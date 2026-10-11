package gate

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// stampRefusal stands in for the *testing.T handed to authntest.Context so that
// a refusal can be observed without failing the case that provokes it.
type stampRefusal struct {
	testing.TB
	refused bool
}

func (*stampRefusal) Helper()                 {}
func (s *stampRefusal) Fatalf(string, ...any) { s.refused = true }

// stampedByAuthntest reports whether authntest.Context accepts the options a
// principal of the given backend name maps to.
func stampedByAuthntest(p authn.Principal, backendName string) bool {
	tb := &stampRefusal{}
	authntest.Context(tb, context.Background(),
		authntest.WithAccount(p.Account), authntest.WithActor(p.Actor), authntest.WithSession(p.Session),
		authntest.WithMethods(p.Methods...), authntest.WithAssurance(p.Assurance),
		authntest.WithIDPAssured(p.IDPAssured), authntest.WithAuthTime(p.AuthTime),
		authntest.WithBackend(backendName), authntest.WithProvider(p.Provider),
		authntest.WithScope(p.Scope), authntest.WithAttributes(p.Attributes))
	return !tb.refused
}

// bytesOf returns sixteen attributes whose keys and values come to size bytes.
func bytesOf(size int) map[string]string {
	m := make(map[string]string, 16)
	rest := size - 16*3
	for i := range 16 {
		n := rest / (16 - i)
		m[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", n)
		rest -= n
	}
	return m
}

// TestAuthntestAndTheExitCheckAgreeOnEveryShapeAndBound holds the two copies of
// the shape and bound rules to one another: the same principal is put to
// checkPrincipal and to authntest.Context, and they have to accept or refuse
// together. The conditions only one of them can decide, the authentication-time
// skew and the namespaces and capabilities of backends other than static, are
// not in the table.
func TestAuthntestAndTheExitCheckAgreeOnEveryShapeAndBound(t *testing.T) {
	d := exitTable(t)
	capable, static := backendNamed(t, d, "capable"), backendNamed(t, d, "static")
	provider := "0198f0aa-0000-7000-8000-000000000001"

	with := func(edit func(*authn.Principal)) authn.Principal {
		p := wellFormed()
		edit(&p)
		return p
	}
	imp := func(edit func(*authn.Principal)) authn.Principal {
		p := impersonating()
		edit(&p)
		return p
	}
	sso := func(edit func(*authn.Principal)) authn.Principal {
		p := wellFormed()
		p.Provider = provider
		edit(&p)
		return p
	}
	attrs := func(m map[string]string) authn.Principal { return with(func(p *authn.Principal) { p.Attributes = m }) }
	eight := []authn.Method{
		authn.MethodPassword, authn.MethodSMS, authn.MethodTOTP, authn.MethodWebAuthn,
		authn.MethodOAuth, authn.MethodOIDC, authn.MethodSAML, authn.MethodClientSecret,
	}
	many := func(n int) map[string]string {
		m := map[string]string{}
		for i := range n {
			m[fmt.Sprintf("k%02d", i)] = "v"
		}
		return m
	}

	for _, c := range []struct {
		name string
		p    authn.Principal
		b    *backend
	}{
		{"a plain principal", wellFormed(), capable},
		{"an account of 64 bytes", with(func(p *authn.Principal) { p.Account = authn.AccountID(strings.Repeat("a", 64)) }), capable},
		{"an account of 65 bytes", with(func(p *authn.Principal) { p.Account = authn.AccountID(strings.Repeat("a", 65)) }), capable},
		{"an empty account", with(func(p *authn.Principal) { p.Account = "" }), capable},
		{"an account with a slash", with(func(p *authn.Principal) { p.Account = "a/b" }), capable},
		{"an account with a space", with(func(p *authn.Principal) { p.Account = "a b" }), capable},
		{"a static account on the static backend", with(func(p *authn.Principal) { p.Account = "static.ops" }), static},
		{"a library account on the static backend", wellFormed(), static},
		{"a static account on another backend", with(func(p *authn.Principal) { p.Account = "static.ops" }), capable},
		{"an impersonation", impersonating(), capable},
		{"an actor that is the account", imp(func(p *authn.Principal) { p.Actor = p.Account }), capable},
		{"an actor without a session", imp(func(p *authn.Principal) { p.Session = "" }), capable},
		{"an actor without a scope", imp(func(p *authn.Principal) { p.Scope = "" }), capable},
		{"a scope without an actor", with(func(p *authn.Principal) { p.Scope = "tenant-a" }), capable},
		{"a scope with a space", imp(func(p *authn.Principal) { p.Scope = "tenant a" }), capable},
		{"a scope with a tab", imp(func(p *authn.Principal) { p.Scope = "tenant\ta" }), capable},
		{"a scope with a NUL", imp(func(p *authn.Principal) { p.Scope = "tenant\x00a" }), capable},
		{"an actor of the static namespace", imp(func(p *authn.Principal) { p.Actor = "static.ops" }), capable},
		{"no methods", with(func(p *authn.Principal) { p.Methods = nil }), capable},
		{"eight methods", with(func(p *authn.Principal) { p.Methods = eight }), capable},
		{"nine methods", with(func(p *authn.Principal) { p.Methods = append(slices.Clone(eight), authn.MethodStatic) }), capable},
		{"a repeated method", with(func(p *authn.Principal) { p.Methods = []authn.Method{authn.MethodStatic, authn.MethodStatic} }), capable},
		{"a method outside the set", with(func(p *authn.Principal) { p.Methods = []authn.Method{"telepathy"} }), capable},
		{"an assurance not stated", with(func(p *authn.Principal) { p.Assurance = authn.AssuranceNone }), capable},
		{"multi-factor", with(func(p *authn.Principal) { p.Assurance = authn.AssuranceMulti }), capable},
		{"an assurance above multi", with(func(p *authn.Principal) { p.Assurance = authn.Assurance(9) }), capable},
		{"an IdP assurance with a provider", sso(func(p *authn.Principal) { p.IDPAssured, p.Assurance = true, authn.AssuranceMulti }), capable},
		{"an IdP assurance without a provider", with(func(p *authn.Principal) { p.IDPAssured, p.Assurance = true, authn.AssuranceMulti }), capable},
		{"an IdP assurance on a single factor", sso(func(p *authn.Principal) { p.IDPAssured = true }), capable},
		{"the zero authentication time", with(func(p *authn.Principal) { p.AuthTime = time.Time{} }), capable},
		{"a provider in canonical form", sso(func(*authn.Principal) {}), capable},
		{"a provider in upper case", sso(func(p *authn.Principal) { p.Provider = strings.ToUpper(provider) }), capable},
		{"a provider in mixed case", sso(func(p *authn.Principal) { p.Provider = "0198F0aa-0000-7000-8000-000000000001" }), capable},
		{"a provider without dashes", sso(func(p *authn.Principal) { p.Provider = strings.ReplaceAll(provider, "-", "") }), capable},
		{"a provider in braces", sso(func(p *authn.Principal) { p.Provider = "{" + provider + "}" }), capable},
		{"a provider of a non-hex digit", sso(func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-00000000000g" }), capable},
		{"a provider that is a word", sso(func(p *authn.Principal) { p.Provider = "idp-1" }), capable},
		{"32 attributes", attrs(many(32)), capable},
		{"33 attributes", attrs(many(33)), capable},
		{"an attribute key of 64 bytes", attrs(map[string]string{strings.Repeat("k", 64): "v"}), capable},
		{"an attribute key of 65 bytes", attrs(map[string]string{strings.Repeat("k", 65): "v"}), capable},
		{"an attribute key in upper case", attrs(map[string]string{"Region": "eu"}), capable},
		{"an attribute key that starts with a dash", attrs(map[string]string{"-k": "v"}), capable},
		{"an attribute key with an underscore and a dot", attrs(map[string]string{"a_b.c-d": "v"}), capable},
		{"an empty attribute key", attrs(map[string]string{"": "v"}), capable},
		{"an attribute under the authn prefix", attrs(map[string]string{"authn.internal": "x"}), capable},
		{"an attribute under the idp prefix", attrs(map[string]string{"idp.group": "eng"}), capable},
		{"an attribute value of 256 bytes", attrs(map[string]string{"k": strings.Repeat("v", 256)}), capable},
		{"an attribute value of 257 bytes", attrs(map[string]string{"k": strings.Repeat("v", 257)}), capable},
		{"an attribute value with a control character", attrs(map[string]string{"k": "a\x00b"}), capable},
		{"an attribute value with a tab", attrs(map[string]string{"k": "a\tb"}), capable},
		{"an attribute value that is not UTF-8", attrs(map[string]string{"k": "\xff"}), capable},
		{"an attribute value of multi-byte text", attrs(map[string]string{"k": "组织"}), capable},
		{"attributes of 4096 bytes", attrs(bytesOf(4096)), capable},
		{"attributes of 4097 bytes", attrs(bytesOf(4097)), capable},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, reason := checkPrincipal(c.p, anchor, c.b, d)
			gate, test := reason == "", stampedByAuthntest(c.p, c.b.name)
			if gate != test {
				t.Errorf("the exit check accepts it: %v (reason %q); authntest accepts it: %v", gate, reason, test)
			}
		})
	}
}
