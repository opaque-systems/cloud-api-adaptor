// (C) Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package gcp

import (
	"strings"

	provider "github.com/confidential-containers/cloud-api-adaptor/src/cloud-providers"
	"github.com/confidential-containers/cloud-api-adaptor/src/cloud-providers/util"
)

type machineTypes []string

func (m *machineTypes) String() string {
	return strings.Join(*m, ", ")
}

func (m *machineTypes) Set(value string) error {
	if len(value) == 0 {
		*m = make(machineTypes, 0)
	} else {
		*m = append(*m, strings.Split(value, ",")...)
	}
	return nil
}

type Config struct {
	GcpCredentials   string
	ProjectID        string
	Zone             string
	ImageName        string
	MachineType      string
	Network          string
	Subnetwork       string
	DiskType         string
	DisableCVM       bool
	ConfidentialType string
	RootVolumeSize   int
	Tags             provider.KeyValueFlag
	UsePublicIP      bool

	// PullRegistry is the single registry hostname (e.g.
	// "us-central1-docker.pkg.dev") that podvm image pulls are
	// authenticated against using a short-lived OAuth token minted from
	// CAA's cloud identity. Empty disables the image-pull-auth feature.
	//
	// One token authenticates every GCP-hosted registry, so a single
	// host key is sufficient when all GCP-hosted images a pod pulls live
	// under one host. Non-GCP registries continue to use the operator's
	// imagePullSecrets.
	PullRegistry string

	// PullImpersonate optionally names a service account to impersonate
	// when minting the pull token. The recommended configuration is a
	// dedicated, least-privilege SA holding only
	// roles/artifactregistry.reader on PullRegistry: the minted token is
	// embedded in the guest's auth.json, so it should carry no more
	// authority than reading the registry. CAA's own identity must hold
	// roles/iam.serviceAccountTokenCreator on this SA.
	//
	// If empty, the token is minted directly from CAA's ADC identity.
	// That is only safe when CAA's identity is itself low-privilege; if
	// it can manage VMs, do NOT use this path, as it embeds a token with
	// CAA's full authority into every podvm.
	PullImpersonate string

	MachineTypes        machineTypes
	MachineTypeSpecList []provider.InstanceTypeSpec
}

func (c Config) Redact() Config {
	return *util.RedactStruct(&c, "GcpCredentials").(*Config)
}
