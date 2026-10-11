package token

import (
	"encoding/json"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// The type of an access token, written into the protected header as "typ". The
// two spellings are the ones RFC 9068 fixes, "at+jwt" and its longer form, and
// either is accepted when a token is read.
const (
	accessTokenType    = "at+jwt"
	accessTokenTypeAlt = "application/at+jwt"
)

// The characters and bounds of the reserved claim names. All of them are ASCII
// and short, so the closed set below is written as its members.
//
// reservedClaims is the closed set of claim names an access token carries. A
// ClaimsContributor that returns any of them is refused with ErrReservedClaim:
// the standard claims are the module's to fill, and a contributor that could
// write one could overwrite who the token is for.
var reservedClaims = map[string]struct{}{
	"iss":        {},
	"sub":        {},
	"aud":        {},
	"exp":        {},
	"nbf":        {},
	"iat":        {},
	"jti":        {},
	"sid":        {},
	"amr":        {},
	"acr":        {},
	"auth_time":  {},
	"act":        {},
	"imp":        {},
	"imp_tenant": {},
	"client_id":  {},
	"typ":        {},
}

// actorClaim is the "act" claim of an impersonation token: the account that
// acted, as RFC 8693 §4.1 writes it.
type actorClaim struct {
	Subject string `json:"sub"`
}

// tokenClaims is the claim set of an access token. The fields carry the
// standard claims the module fills; a ClaimsContributor's claims are merged in
// beside them after their names have been held to reservedClaims.
//
// It is a struct rather than a map so that the issuing path cannot drop a
// required claim by forgetting a key: every field is written explicitly, and
// the JSON tags fix the wire names. Extra carries what the contributors added
// and is never filled by the module itself.
type tokenClaims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  []string `json:"aud"`
	Expiry    int64    `json:"exp"`
	NotBefore int64    `json:"nbf"`
	IssuedAt  int64    `json:"iat"`
	ID        string   `json:"jti"`
	Session   string   `json:"sid"`

	Methods   []string `json:"amr,omitempty"`
	Assurance string   `json:"acr,omitempty"`
	AuthTime  int64    `json:"auth_time,omitempty"`

	ClientID string `json:"client_id"`

	// The impersonation claims, present only on an impersonation token.
	Actor               *actorClaim `json:"act,omitempty"`
	Impersonation       string      `json:"imp,omitempty"`
	ImpersonationTenant string      `json:"imp_tenant,omitempty"`

	// Extra holds the claims a ClaimsContributor added. It is marshalled
	// beside the fields above and is empty on every token without a
	// contributor.
	Extra map[string]any `json:"-"`
}

// checkContributorClaims refuses a contributor's map that names any reserved
// claim. The map may be nil, which adds nothing. The whole issue fails on the
// first reserved name, so no token is signed from a partial set.
func checkContributorClaims(extra map[string]any) error {
	for name := range extra {
		if _, reserved := reservedClaims[name]; reserved {
			return fmt.Errorf("%w: a claims contributor returned %q", authn.ErrReservedClaim, name)
		}
	}
	return nil
}

// marshal writes the token's claims as JSON, with the contributors' claims
// merged in beside the standard ones. The reserved names are held out here as
// well as by checkContributorClaims, so a merge that would overwrite a standard
// claim is refused rather than silently winning.
func (c tokenClaims) marshal() ([]byte, error) {
	standard, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if len(c.Extra) == 0 {
		return standard, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(standard, &merged); err != nil {
		return nil, err
	}
	for name, value := range c.Extra {
		if _, reserved := reservedClaims[name]; reserved {
			return nil, fmt.Errorf("%w: a claims contributor returned %q", authn.ErrReservedClaim, name)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("%s: marshalling the claim %q: %w", ModuleName, name, err)
		}
		merged[name] = encoded
	}
	return json.Marshal(merged)
}
