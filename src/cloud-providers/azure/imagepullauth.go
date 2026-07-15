// (C) Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// acrPullAuthScope is the AAD scope requested for the token exchanged
// with ACR. ACR is the resource being authenticated to; the exchanged
// refresh token's actual authority is bounded by the AcrPull role
// assignment held by the identity it was minted for (see
// Config.PullIdentity), not by this scope.
const acrPullAuthScope = "https://containerregistry.azure.net/.default"

// acrRefreshTokenUsername is the fixed sentinel docker/containerd (and
// `az acr login`) use in a docker-config-json "auth" field to signal
// "this password is an ACR refresh token, not a real username/password
// pair" -- the registry's own bearer-challenge flow exchanges it for a
// repository-scoped access token on each pull, so CAA only ever has to
// hand over the refresh token, not a pre-scoped access token.
const acrRefreshTokenUsername = "00000000-0000-0000-0000-000000000000"

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

// AugmentImagePullAuth mints an ACR refresh token from CAA's cloud
// identity (optionally impersonating Config.PullIdentity) and emits a
// docker-config-json document that authenticates pulls from
// Config.PullRegistry as that identity. Implements
// provider.ImagePullAuthAugmenter.
//
// Refresh tokens live ~3h. They are shipped into the podvm at create
// time and consumed by CDH at container-create time, typically seconds
// after VM boot -- well inside the TTL. kata-remote does not re-pull on
// container restart, so a single token per VM is sufficient.
func (p *azureProvider) AugmentImagePullAuth(ctx context.Context) ([]byte, error) {
	host := p.serviceConfig.PullRegistry
	if host == "" {
		// Feature disabled; no error so the caller treats "unsupported"
		// and "configured off" identically.
		return nil, nil
	}

	refreshToken, err := p.mintACRRefreshToken(ctx, host)
	if err != nil {
		return nil, err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(acrRefreshTokenUsername + ":" + refreshToken))

	return json.Marshal(dockerConfigJSON{
		Auths: map[string]dockerAuthEntry{host: {Auth: auth}},
	})
}

// pullTokenCredential returns a credential that requests an AAD token
// for PullIdentity's client ID specifically -- Azure Workload Identity
// has no impersonation primitive, so this only works when PullIdentity's
// managed identity has its own federated credential trusting CAA's K8s
// ServiceAccount (same OIDC issuer + subject as CAA's own identity).
//
// ConfigVerifier requires PullIdentity and PullRegistry to be set
// together, so there is no "reuse CAA's own credential" fallback here.
func (p *azureProvider) pullTokenCredential() (azcore.TokenCredential, error) {
	cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
		ClientID: p.serviceConfig.PullIdentity,
	})
	if err != nil {
		return nil, fmt.Errorf("workload identity credential for %s: %w", p.serviceConfig.PullIdentity, err)
	}
	return cred, nil
}

// mintACRRefreshToken obtains an AAD access token via pullTokenCredential
// and exchanges it for a registry-scoped ACR refresh token by calling
// the registry's own OAuth2 exchange endpoint -- the same exchange `az
// acr login` and docker-credential-acr-env perform.
func (p *azureProvider) mintACRRefreshToken(ctx context.Context, host string) (string, error) {
	cred, err := p.pullTokenCredential()
	if err != nil {
		return "", fmt.Errorf("building image-pull credential: %w", err)
	}

	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{acrPullAuthScope}})
	if err != nil {
		return "", fmt.Errorf("mint AAD token for image-pull: %w", err)
	}

	form := url.Values{
		"grant_type":   {"access_token"},
		"service":      {host},
		"access_token": {tok.Token},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/oauth2/exchange", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building ACR exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling ACR exchange endpoint at %s: %w", host, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading ACR exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ACR exchange at %s returned %s: %s", host, resp.Status, body)
	}

	var exchangeResp struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &exchangeResp); err != nil {
		return "", fmt.Errorf("parsing ACR exchange response: %w", err)
	}
	if exchangeResp.RefreshToken == "" {
		return "", fmt.Errorf("ACR exchange at %s returned no refresh_token", host)
	}

	return exchangeResp.RefreshToken, nil
}
