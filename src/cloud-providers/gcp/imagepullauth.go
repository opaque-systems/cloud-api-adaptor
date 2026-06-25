// (C) Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"
)

// pullAuthScope is the OAuth scope requested for the image-pull token.
// Artifact Registry / gcr.io accept cloud-platform; the token's actual
// authority is bounded by the IAM roles of the identity it is minted
// for (see Config.PullImpersonate), not by the scope.
const pullAuthScope = "https://www.googleapis.com/auth/cloud-platform"

// dockerConfigJSON mirrors the on-wire shape of a Kubernetes
// dockerconfigjson payload -- {"auths": {"<host>": {"auth":
// "<base64(user:pass)>"}}} -- so the bytes returned here merge 1:1 with
// the imagePullSecrets-derived bytes CAA already builds.
type dockerConfigJSON struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Auth string `json:"auth"`
}

// AugmentImagePullAuth mints a short-lived OAuth token from CAA's cloud
// identity (optionally impersonating Config.PullImpersonate) and emits a
// docker-config-json document that authenticates pulls from
// Config.PullRegistry as that identity. Implements
// provider.ImagePullAuthAugmenter.
//
// Tokens live ~1h. They are shipped into the podvm at create time and
// consumed by CDH at container-create time, typically seconds after VM
// boot -- well inside the TTL. kata-remote does not re-pull on container
// restart, so a single token per VM is sufficient.
func (p *gcpProvider) AugmentImagePullAuth(ctx context.Context) ([]byte, error) {
	host := p.serviceConfig.PullRegistry
	if host == "" {
		// Feature disabled; no error so the caller treats "unsupported"
		// and "configured off" identically.
		return nil, nil
	}

	ts, err := p.pullTokenSource(ctx)
	if err != nil {
		return nil, err
	}

	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("mint image-pull token: %w", err)
	}

	auth := base64.StdEncoding.EncodeToString([]byte("oauth2accesstoken:" + tok.AccessToken))

	return json.Marshal(dockerConfigJSON{
		Auths: map[string]dockerAuthEntry{host: {Auth: auth}},
	})
}

// pullTokenSource returns the token source used to mint the image-pull
// token. When PullImpersonate is set it returns an impersonated source
// (recommended: a least-privilege pull SA); otherwise it returns CAA's
// own ADC token source. When GCP_CREDENTIALS is configured it is used as
// the base credential, matching how NewProvider builds the compute
// client; otherwise Application Default Credentials are used.
func (p *gcpProvider) pullTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	var opts []option.ClientOption
	if p.serviceConfig.GcpCredentials != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(p.serviceConfig.GcpCredentials)))
	}

	if sa := p.serviceConfig.PullImpersonate; sa != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: sa,
			Scopes:          []string{pullAuthScope},
		}, opts...)
		if err != nil {
			return nil, fmt.Errorf("impersonate %s for image-pull token: %w", sa, err)
		}
		return ts, nil
	}

	if p.serviceConfig.GcpCredentials != "" {
		creds, err := google.CredentialsFromJSON(ctx, []byte(p.serviceConfig.GcpCredentials), pullAuthScope)
		if err != nil {
			return nil, fmt.Errorf("image-pull creds from GCP_CREDENTIALS: %w", err)
		}
		return creds.TokenSource, nil
	}

	ts, err := google.DefaultTokenSource(ctx, pullAuthScope)
	if err != nil {
		return nil, fmt.Errorf("image-pull ADC token source: %w", err)
	}
	return ts, nil
}
