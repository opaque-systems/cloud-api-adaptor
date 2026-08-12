// (C) Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package azure

import (
	"strings"

	provider "github.com/confidential-containers/cloud-api-adaptor/src/cloud-providers"
	"github.com/confidential-containers/cloud-api-adaptor/src/cloud-providers/util"
)

type instanceSizes []string

func (i *instanceSizes) String() string {
	return strings.Join(*i, ", ")
}

func (i *instanceSizes) Set(value string) error {
	if len(value) == 0 {
		*i = make(instanceSizes, 0)
	} else {
		*i = append(*i, strings.Split(value, ",")...)
	}
	return nil
}

type Config struct {
	SubscriptionID       string
	ClientID             string
	ClientSecret         string
	TenantID             string
	ResourceGroupName    string
	Zone                 string
	Region               string
	SubnetID             string
	SecurityGroupName    string
	SecurityGroupID      string
	Size                 string
	ImageID              string
	SSHKeyPath           string
	SSHUserName          string
	DisableCVM           bool
	InstanceSizes        instanceSizes
	InstanceSizeSpecList []provider.InstanceTypeSpec
	Tags                 provider.KeyValueFlag
	DisableCloudConfig   bool
	// Disabled by default, we want to do measured boot.
	// Secure boot brings no additional security.
	EnableSecureBoot bool
	UsePublicIP      bool
	RootVolumeSize   int

	// PullRegistry is the single registry hostname (e.g.
	// "myregistry.azurecr.io") that podvm image pulls are authenticated
	// against using a short-lived ACR refresh token minted from CAA's
	// cloud identity. Empty disables the image-pull-auth feature.
	//
	// One refresh token authenticates every repository under that ACR
	// instance (the docker registry-v2 bearer challenge scopes it down
	// per-pull), so a single host key is sufficient when all
	// ACR-hosted images a pod pulls live under one registry. Non-ACR
	// registries continue to use the operator's imagePullSecrets.
	PullRegistry string

	// PullIdentity optionally names the client ID of a user-assigned
	// managed identity to request the AAD token for, instead of CAA's
	// own identity. The recommended configuration is a dedicated,
	// least-privilege identity holding only AcrPull on PullRegistry:
	// the minted refresh token is embedded in the guest's auth.json, so
	// it should carry no more authority than reading the registry.
	//
	// Unlike GCP's service-account impersonation, Azure Workload
	// Identity has no "impersonate any identity you hold a role on"
	// primitive: PullIdentity's managed identity must have its own
	// federated credential trusting CAA's K8s ServiceAccount (the same
	// OIDC issuer + subject CAA's own identity is federated to).
	//
	// If empty, the token is minted directly from CAA's own workload
	// identity. That is only safe when CAA's identity is itself
	// low-privilege; if it can manage VMs, do NOT use this path, as it
	// embeds a token with CAA's full authority into every podvm.
	PullIdentity string
}

func (c Config) Redact() Config {
	return *util.RedactStruct(&c, "ClientID", "TenantID", "ClientSecret").(*Config)
}
