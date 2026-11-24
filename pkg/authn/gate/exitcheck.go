package gate

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
)

// The bounds the exit check holds a principal to. They are constants of the
// gateway, not input items: a deployment that could widen them could widen what
// a backend is believed about.
const (
	// authTimeSkew is how far ahead of the gateway's clock a principal's
	// AuthTime may be. It allows for two hosts whose clocks differ a little and
	// no more; an authentication that claims to be further ahead has not
	// happened yet.
	authTimeSkew      = 120 * time.Second
	maxMethods        = 8
	maxAttributes     = 32
	maxAttributeBytes = 4 << 10
	maxAttributeValue = 256
	maxAttributeKey   = 64
)

// checkPrincipal holds what a backend claimed about a caller to the shapes,
// bounds and declarations the gateway accepts, and returns the principal to
// stamp an identity from. The second result is empty when every check held and
// otherwise names the first one that did not, without echoing any value the
// backend put there.
//
// The principal comes back with its assurance settled: the zero value reads as
// not stated, which is stamped as a single factor, the lowest a verified
// credential can have.
//
// The backend decides nothing here: what it may claim is what it declared in
// Accepts. An account must lie in the namespace it declared, and an actor, an
// impersonation scope, an SSO provider and an assurance an IdP declared are
// facts only a backend that declared the capability could assert. A scope
// belongs to an impersonation: one named with no actor is a state no backend
// has a reason to produce, and it is refused rather than read as a narrowing.
func checkPrincipal(p authn.Principal, now time.Time, b *backend, d *dispatch) (authn.Principal, string) {
	if !validBackendName(b.name) {
		return p, "the backend's name is not the shape a backend name has"
	}
	if _, err := authn.ParseAccountID(string(p.Account)); err != nil {
		return p, "the account is not a well-formed account identifier"
	}
	if p.Actor != "" {
		if _, err := authn.ParseAccountID(string(p.Actor)); err != nil {
			return p, "the actor is not a well-formed account identifier"
		}
		if p.Actor == p.Account {
			return p, "the actor is the account itself, which is no impersonation"
		}
		if p.Session == "" || p.Scope == "" {
			return p, "the principal names an actor without the session and the scope an " +
				"impersonation carries"
		}
	}
	if p.Actor == "" && p.Scope != "" {
		return p, "the principal names an impersonation scope without an actor to confine"
	}
	if reason := checkMethods(p.Methods); reason != "" {
		return p, reason
	}
	if p.Assurance > authn.AssuranceMulti {
		return p, "the assurance is not one of the defined levels"
	}
	if p.Assurance == authn.AssuranceNone {
		p.Assurance = authn.AssuranceSingle
	}
	if p.IDPAssured && (p.Assurance != authn.AssuranceMulti || p.Provider == "") {
		return p, "the principal declares an IdP's multi-factor assurance without that assurance " +
			"level or without the provider"
	}
	if p.AuthTime.IsZero() {
		return p, "the authentication time is not set"
	}
	if p.AuthTime.After(now.Add(authTimeSkew)) {
		return p, "the authentication time is ahead of the gateway's clock by more than the " +
			"allowed skew"
	}
	if p.Scope != "" && strings.ContainsFunc(p.Scope, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return p, "the impersonation scope is not the shape of a tenant identifier"
	}
	if p.Provider != "" && !uuidShape(p.Provider) {
		return p, "the SSO provider is not UUID text in its canonical form"
	}
	if reason := checkAttributes(p.Attributes); reason != "" {
		return p, reason
	}
	if !d.inNamespace(b, p.Account) || (p.Actor != "" && !d.inNamespace(b, p.Actor)) {
		return p, "an account lies outside the account namespace the backend declared"
	}
	if (p.Actor != "" || p.Scope != "") && !b.impersonation {
		return p, "the principal names an actor or an impersonation scope, which this backend " +
			"did not declare it may"
	}
	if (p.Provider != "" || p.IDPAssured) && !b.sso {
		return p, "the principal names an SSO provider or an assurance an IdP declared, which " +
			"this backend did not declare it may"
	}
	return p, ""
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// uuidShape reports whether s is UUID text in its canonical form: 32 lower-case
// hex digits in the 8-4-4-4-12 grouping. It is not uuid.Parse, which also takes
// braces, a urn prefix, no dashes and capitals: a provider identifier is
// compared as a string wherever it is read, so one value has one spelling.
func uuidShape(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHexDigit(c) {
				return false
			}
		}
	}
	return true
}

func checkMethods(methods []authn.Method) string {
	if len(methods) == 0 {
		return "the principal names no authentication method"
	}
	if len(methods) > maxMethods {
		return "the principal names more authentication methods than the bound allows"
	}
	seen := make(map[authn.Method]struct{}, len(methods))
	for _, m := range methods {
		if !m.Valid() {
			return "the principal names an authentication method outside the closed set"
		}
		if _, repeated := seen[m]; repeated {
			return "the principal names an authentication method twice"
		}
		seen[m] = struct{}{}
	}
	return ""
}

func checkAttributes(attrs map[string]string) string {
	if len(attrs) > maxAttributes {
		return "the attribute set has more entries than the bound allows"
	}
	total := 0
	for k, v := range attrs {
		if !attributeKey(k) {
			return "an attribute key is not spelt the way a key is"
		}
		if len(v) > maxAttributeValue {
			return "an attribute value is longer than the bound allows"
		}
		if !utf8.ValidString(v) || strings.ContainsFunc(v, unicode.IsControl) {
			return "an attribute value is not valid UTF-8 free of control characters"
		}
		total += len(k) + len(v)
	}
	if total > maxAttributeBytes {
		return "the attribute set is larger than the bound allows"
	}
	return ""
}

func attributeKey(k string) bool {
	if k == "" || len(k) > maxAttributeKey {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return true
}
