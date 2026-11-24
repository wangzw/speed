package http

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

type probe interface{ Probe() }

type concreteProbe struct{}

// wantPanic runs fn and returns the panic text, failing when fn returns normally. The text is handed back rather than asserted here: this module panics where the mistake is a call-site one, and a refusal that says nothing about which call was refused leaves the author where they were.
func wantPanic(t *testing.T, what string, fn func()) string {
	t.Helper()
	var text string
	func() {
		defer func() {
			if r := recover(); r != nil {
				text, _ = r.(string)
			}
		}()
		fn()
		t.Fatalf("%s returned instead of panicking", what)
	}()
	if text == "" {
		t.Fatalf("%s panicked with something other than a message string", what)
	}
	return text
}

// assertNames checks that the panic text carries the offending declaration and
// the fixing action. Refusing without saying what to write instead leaves the
// author of the declaration exactly where they were.
func assertNames(t *testing.T, text string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("the panic text %q does not carry %q", text, fragment)
		}
	}
}

// TestTokenRejectsUntypedNil checks that an untyped nil token is refused.
func TestTokenRejectsUntypedNil(t *testing.T) {
	text := wantPanic(t, "an untyped nil token", func() {
		capabilityKey(nil, `Use on endpoint "public"`)
	})
	assertNames(t, text, `Use on endpoint "public"`, "untyped nil", "(*auth.Authenticator)(nil)")
}

// TestTokenRejectsNonPointer checks that a token that is not a pointer is refused.
func TestTokenRejectsNonPointer(t *testing.T) {
	text := wantPanic(t, "a non-pointer token", func() {
		capabilityKey(concreteProbe{}, `Use on endpoint "public"`)
	})
	assertNames(t, text, `Use on endpoint "public"`, "must be a typed nil pointer", "http.concreteProbe")
}

// TestTokenRejectsNonNilPointer checks that a non-nil pointer token is refused.
func TestTokenRejectsNonNilPointer(t *testing.T) {
	text := wantPanic(t, "a non-nil pointer token", func() {
		capabilityKey(&concreteProbe{}, `Use on endpoint "public"`)
	})
	assertNames(t, text, `Use on endpoint "public"`, "must be a nil pointer", "not an instance")
}

// TestTokenRejectsConcreteElement checks that a token designating a concrete type is refused.
func TestTokenRejectsConcreteElement(t *testing.T) {
	text := wantPanic(t, "a token pointing at a concrete type", func() {
		capabilityKey((*concreteProbe)(nil), `Use on endpoint "public"`)
	})
	assertNames(t, text, `Use on endpoint "public"`, "must point to an interface", "concreteProbe")
}

// TestTokenAcceptsInterfacePointer is the positive half: the four refusals above would all still
// hold in an implementation that refuses everything.
func TestTokenAcceptsInterfacePointer(t *testing.T) {
	got := capabilityKey((*probe)(nil), `Use on endpoint "public"`)
	if got == nil {
		t.Fatal("a legal capability token yielded no key")
	}
	if got.Name() != "probe" {
		t.Errorf("the key is %s, want the interface type probe", got)
	}
	if same := capabilityKey((*probe)(nil), "another site"); same != got {
		t.Error("two tokens designating one capability yielded different keys, " +
			"so the middleware graph would treat them as two capabilities")
	}
}

// panics reports whether fn panicked, without judging that either way. It is
// the shape the cross-check below needs: there both verdicts are observations,
// and it is their agreement that is asserted.
func panics(fn func()) (did bool) {
	defer func() {
		if recover() != nil {
			did = true
		}
	}()
	fn()
	return false
}

// TestCapabilityRuleAgreesWithCore holds this module's copy of the token rule against core's.
func TestCapabilityRuleAgreesWithCore(t *testing.T) {
	var interfaceValue probe
	cases := []struct {
		name    string
		token   core.Token
		illegal bool
	}{
		{name: "an untyped nil", token: nil, illegal: true},
		{name: "a non-pointer", token: concreteProbe{}, illegal: true},
		{name: "a nil channel of an interface", token: (chan probe)(nil), illegal: true},
		{name: "a non-nil pointer", token: &concreteProbe{}, illegal: true},
		{name: "a non-nil pointer to an interface", token: &interfaceValue, illegal: true},
		{name: "a pointer to a concrete type", token: (*concreteProbe)(nil), illegal: true},
		{name: "a pointer to an interface", token: (*probe)(nil), illegal: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A fresh registry per row: a name collision panics too, and
			// reusing one would read as a verdict on the token.
			byCore := panics(func() {
				core.New().Register(core.Module{
					Name:     "probe-module",
					Requires: []core.Requirement{{Token: c.token}},
				})
			})
			byModule := panics(func() {
				capabilityKey(c.token, `Use on endpoint "public"`)
			})
			if byCore != byModule {
				t.Fatalf("%s: core.Register panicked=%v, capabilityKey panicked=%v. "+
					"The two copies of the capability token rule have drifted apart",
					c.name, byCore, byModule)
			}
			if byModule != c.illegal {
				t.Fatalf("%s: both refused=%v, want %v. "+
					"core and this module agree, so the rule itself has moved",
					c.name, byModule, c.illegal)
			}
		})
	}
}
