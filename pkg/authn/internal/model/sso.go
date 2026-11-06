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

	// UsernameStrategyClaim takes the username from a claim the provider's
	// assertion carries; UsernameStrategyUserConfirm asks the person at the
	// first just-in-time login, before any account is written.
	UsernameStrategyClaim       = "claim"
	UsernameStrategyUserConfirm = "user-confirm"

	// UsernameClaimPreferredUsername reads the standard preferred_username
	// claim; UsernameClaimEmail reads the local part of the asserted address.
	UsernameClaimPreferredUsername = "preferred_username"
	UsernameClaimEmail             = "email"
)

// SSOProvider is one enterprise IdP of a tenant. What is read out of the store
// never holds the client secret, and Config holds no secret either.
type SSOProvider struct {
	ID           string
	Tenant       string
	Protocol     string // SSOProtocolOIDC or SSOProtocolSAML
	Name         string
	Enabled      bool   // the zero value reads as disabled
	Provisioning string // ProvisioningJIT or ProvisioningPreprovisioned
	// UsernameStrategy is how a just-in-time login determines the username it
	// opens the account with. It is empty for a provider that never provisions.
	UsernameStrategy string
	// UsernameClaim is the claim UsernameStrategyClaim reads. It is empty for
	// every other strategy.
	UsernameClaim     string
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

// SSOProviderDomain is one email domain a provider has claimed. Verified reads
// true only once the domain owner has published the claim's token in the DNS
// TXT record; an unverified claim takes no part in domain discovery.
type SSOProviderDomain struct {
	Domain   string
	Verified bool
}
