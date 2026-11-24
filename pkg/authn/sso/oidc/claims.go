package oidc

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const (
	targetSubject     = "subject"
	targetEmail       = "email"
	targetDisplayName = "display_name"
	targetAttrPrefix  = "attr."
)

type profile struct {
	subject           string
	email             string
	emailVerified     bool
	displayName       string
	preferredUsername string
	attributes        map[string]string
}

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
	// preferred_username is read as the standard claim itself, because a just-in-time provider may name it as the username source.
	p.preferredUsername = stringClaim(claims["preferred_username"])
	if len(p.attributes) == 0 {
		p.attributes = nil
	}
	return p
}

func stringClaim(v any) string {
	s, _ := v.(string)
	return s
}

func boolClaim(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	}
	return false
}

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
