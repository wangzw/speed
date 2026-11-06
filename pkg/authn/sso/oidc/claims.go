package oidc

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The targets an attribute mapping may name, and the prefix of the ones that
// become read-only attributes. The set is closed and is validated when the
// provider is written.
const (
	targetSubject     = "subject"
	targetEmail       = "email"
	targetDisplayName = "display_name"
	targetAttrPrefix  = "attr."
)

// profile is what an identity token proves about the person, after the mapping.
type profile struct {
	subject           string
	email             string
	emailVerified     bool
	displayName       string
	preferredUsername string
	attributes        map[string]string // keys already carry the reserved idp. prefix
}

// buildProfile maps an identity token's claims to a profile. The mapping names a
// claim and the target it fills; the subject falls back to the token's own
// subject, which is the sub claim, only when no mapping names one. A claim a
// mapping names but the token does not carry leaves its target empty, which the
// caller refuses rather than filling it from somewhere else. Whether the address
// is verified is read from the email_verified claim itself.
func buildProfile(cfg providerConfig, tokenSubject string, claims map[string]any) profile {
	p := profile{subject: tokenSubject, attributes: map[string]string{}}
	for name, target := range cfg.Claims {
		switch target {
		case targetSubject:
			p.subject = stringClaim(claims[name])
		case targetEmail:
			p.email = stringClaim(claims[name])
		case targetDisplayName:
			p.displayName = stringClaim(claims[name])
		default:
			if attr, ok := strings.CutPrefix(target, targetAttrPrefix); ok {
				if v, ok := claims[name].(string); ok {
					p.attributes[authn.ReservedPrefixIDP+attr] = v
				}
			}
		}
	}
	p.emailVerified = boolClaim(claims["email_verified"])
	// preferred_username is read as the standard claim itself, not through the
	// configured mapping: a just-in-time provider may name it as the source of
	// the username it creates the account with, and that source is the token,
	// not a mapping an administrator had to add beside it.
	p.preferredUsername = stringClaim(claims["preferred_username"])
	if len(p.attributes) == 0 {
		p.attributes = nil
	}
	return p
}

// stringClaim reads a claim that is a string; anything else, absent included,
// reads as empty.
func stringClaim(v any) string {
	s, _ := v.(string)
	return s
}

// boolClaim reads a claim that is a JSON boolean, tolerating the string form a
// provider may send instead. Anything else, the absent claim included, reads as
// false.
func boolClaim(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	}
	return false
}

// classifyVerify is the outcome to report for an identity token the verifier
// refused. The library flattens a bad signature, a foreign audience, a foreign
// issuer and an expiry into one error, so the outcome is read off the token's
// own, unverified claims: it labels the metric and decides nothing. Any claim
// that cannot be read as the token's origin did leaves the token a signature
// failure, which is the strongest refusal the library can hand back.
func classifyVerify(raw string, cfg providerConfig, now time.Time) string {
	claims, ok := unverifiedClaims(raw)
	if !ok {
		return outcomeSignature
	}
	if exp, ok := numberClaim(claims["exp"]); ok && time.Unix(exp, 0).Before(now) {
		return outcomeTime
	}
	if !audienceContains(claims["aud"], cfg.ClientID) {
		return outcomeAudience
	}
	if iss, ok := claims["iss"].(string); ok && iss != cfg.Issuer {
		return outcomeIssuer
	}
	return outcomeSignature
}

// unverifiedClaims reads the claim set of a compact JWT without checking its
// signature. It is used for a metric label alone, never for a judgement.
func unverifiedClaims(raw string) (map[string]any, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

// numberClaim reads a claim that is a number.
func numberClaim(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// audienceContains reports whether an aud claim, a string or a list of strings,
// names the client.
func audienceContains(v any, clientID string) bool {
	switch a := v.(type) {
	case string:
		return a == clientID
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}
