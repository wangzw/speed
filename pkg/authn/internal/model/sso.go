package model

import (
	"encoding/json"
	"slices"
)

// The protocols of an enterprise SSO provider, and the ways an account is
// created for a user who signs in through one. Both sets are closed.
const (
	SSOProtocolOIDC = "oidc"
	SSOProtocolSAML = "saml"

	ProvisioningJIT            = "jit"
	ProvisioningPreprovisioned = "preprovisioned"
)

// SSOProvider is one enterprise IdP of a tenant. What is read out of the store
// never holds the client secret, and Config holds no secret either.
type SSOProvider struct {
	ID                string
	Tenant            string
	Protocol          string // SSOProtocolOIDC or SSOProtocolSAML
	Name              string
	Enabled           bool   // the zero value reads as disabled
	Provisioning      string // ProvisioningJIT or ProvisioningPreprovisioned
	TrustIDPMFA       bool
	AllowIDPInitiated bool
	AllowSHA1         bool
	MergeExisting     bool            // whether JIT may merge into an existing account; false by default
	Config            json.RawMessage // holds no secret when read out
	Version           int
}

// Clone returns a copy that shares no slice with p.
func (p SSOProvider) Clone() SSOProvider {
	p.Config = slices.Clone(p.Config)
	return p
}
