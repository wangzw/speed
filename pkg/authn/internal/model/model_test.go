package model

import (
	"errors"
	"strings"
	"testing"
)

// checks that ParseAccountID accepts a well-formed identifier and refuses the
// rest.
func TestParseAccountID(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty", "", false},
		{"one byte", "a", true},
		{"64 bytes", strings.Repeat("a", 64), true},
		{"65 bytes", strings.Repeat("a", 65), false},
		{"uuid text", "0192f6c2-7b1e-7d3a-9c11-5a3f0e2b8d44", true},
		{"every permitted punctuation", "A.b_c-D9", true},
		{"static subject", "static.bob", true},
		{"space", "bob smith", false},
		{"leading space", " bob", false},
		{"trailing newline", "bob\n", false},
		{"slash", "a/b", false},
		{"at sign", "bob@example.com", false},
		{"colon", "idp:bob", false},
		{"NUL", "bob\x00", false},
		{"NUL alone", "\x00", false},
		{"non-ASCII letter", "bób", false},
		{"CJK", "用户", false},
		{"fullwidth digit", "１", false},
		{"64 bytes with one bad byte last", strings.Repeat("a", 63) + "/", false},
		{"65 bytes of permitted characters", strings.Repeat("0", 65), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseAccountID(c.in)
			if c.ok {
				if err != nil || string(got) != c.in {
					t.Fatalf("ParseAccountID(%q) = %q, %v; want it accepted unchanged", c.in, got, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidAccountID) {
				t.Fatalf("ParseAccountID(%q) error = %v; want ErrInvalidAccountID", c.in, err)
			}
			if got != "" {
				t.Fatalf("ParseAccountID(%q) = %q alongside the error; want the empty identifier", c.in, got)
			}
			if c.in != "" && strings.Contains(err.Error(), c.in) {
				t.Fatalf("the failure echoes its input: %v", err)
			}
		})
	}
}

// TestAnInvalidAccountIDFailureSaysWhatItIs checks the failure names authn and
// the account identifier.
func TestAnInvalidAccountIDFailureSaysWhatItIs(t *testing.T) {
	_, err := ParseAccountID("not valid")
	if err == nil {
		t.Fatal("ParseAccountID accepted a space")
	}
	text := err.Error()
	if !strings.HasPrefix(text, "authn: ") || !strings.Contains(text, "account identifier") {
		t.Errorf("the failure reads %q, want it to name authn and the account identifier", text)
	}
}

// checks that IsStatic reports whether an identifier belongs to the static
// table.
func TestAccountIDIsStatic(t *testing.T) {
	for _, c := range []struct {
		id   AccountID
		want bool
	}{
		{"static.bob", true},
		{"static.", true},
		{"static", false},
		{"xstatic.bob", false},
		{"Static.bob", false},
		{"STATIC.bob", false},
		{"static-bob", false},
		{"", false},
		{"0192f6c2-7b1e-7d3a-9c11-5a3f0e2b8d44", false},
	} {
		if got := c.id.IsStatic(); got != c.want {
			t.Errorf("AccountID(%q).IsStatic() = %v, want %v", c.id, got, c.want)
		}
	}
}

// checks that String returns the identifier's text.
func TestAccountIDString(t *testing.T) {
	if got := AccountID("static.bob").String(); got != "static.bob" {
		t.Fatalf("String() = %q", got)
	}
}

// checks that Valid accepts a method in the closed set and refuses one
// outside it.
func TestMethodValid(t *testing.T) {
	for _, m := range []Method{
		MethodPassword, MethodSMS, MethodTOTP, MethodWebAuthn, MethodOAuth,
		MethodOIDC, MethodSAML, MethodStatic, MethodClientSecret,
	} {
		if !m.Valid() {
			t.Errorf("Method(%q).Valid() = false, want true: it is a constant of the closed set", m)
		}
	}
	for _, m := range []Method{"", "PWD", "Pwd", " pwd", "pwd ", "password", "mfa", "otp\x00", "unknown"} {
		if m.Valid() {
			t.Errorf("Method(%q).Valid() = true, want false: it is outside the closed set", m)
		}
	}
}

// The wire texts are fixed by RFC 8176 for three of the values and are what a
// token's amr claim carries.
func TestMethodTexts(t *testing.T) {
	for m, want := range map[Method]string{
		MethodPassword: "pwd", MethodSMS: "sms", MethodTOTP: "otp", MethodWebAuthn: "webauthn",
		MethodOAuth: "oauth", MethodOIDC: "oidc", MethodSAML: "saml", MethodStatic: "static",
		MethodClientSecret: "client-secret",
	} {
		if string(m) != want {
			t.Errorf("method constant reads %q, want %q", m, want)
		}
	}
}

// TestZeroValuesReadAsTheLeastPermissive checks that zero values read as the
// least permissive.
func TestZeroValuesReadAsTheLeastPermissive(t *testing.T) {
	var a Assurance
	if a != AssuranceNone {
		t.Errorf("the zero Assurance is %d, want AssuranceNone", a)
	}
	if AssuranceNone >= AssuranceSingle || AssuranceSingle >= AssuranceMulti {
		t.Error("the assurance levels are not ordered None < Single < Multi")
	}
	var acc Access
	if acc != AccessUnknown {
		t.Errorf("the zero Access is %d, want AccessUnknown", acc)
	}
	var f FactorStatus
	if f != FactorPending {
		t.Errorf("the zero FactorStatus is %d, want FactorPending: a factor not confirmed is not usable", f)
	}
	if FactorActive == FactorPending || FactorDisabled == FactorPending || FactorActive == FactorDisabled {
		t.Error("FactorStatus values collide")
	}
	var r Requirement
	if r.Assurance != AssuranceNone || r.MaxAge != 0 || r.Local {
		t.Error("the zero Requirement asks for something")
	}
	if AccessOpen == AccessUnknown || AccessOptional == AccessUnknown || AccessRequired == AccessUnknown {
		t.Error("an Access value collides with AccessUnknown")
	}
}

// TestPrincipalCloneSharesNothing checks that principal clone shares nothing.
func TestPrincipalCloneSharesNothing(t *testing.T) {
	orig := Principal{
		Account:    "a",
		Methods:    []Method{MethodPassword, MethodTOTP},
		Attributes: map[string]string{"region": "eu"},
	}
	c := orig.Clone()

	c.Methods[0] = MethodSMS
	c.Attributes["region"] = "us"
	c.Attributes["tier"] = "gold"

	if orig.Methods[0] != MethodPassword {
		t.Error("changing the clone's Methods changed the original")
	}
	if orig.Attributes["region"] != "eu" || len(orig.Attributes) != 1 {
		t.Error("changing the clone's Attributes changed the original")
	}

	orig.Methods[1] = MethodWebAuthn
	orig.Attributes["x"] = "y"
	if c.Methods[1] != MethodTOTP {
		t.Error("changing the original's Methods changed the clone")
	}
	if _, ok := c.Attributes["x"]; ok {
		t.Error("changing the original's Attributes changed the clone")
	}
}

// TestCloneKeepsNilNil checks that clone keeps nil nil.
func TestCloneKeepsNilNil(t *testing.T) {
	if c := (Principal{}).Clone(); c.Methods != nil || c.Attributes != nil {
		t.Error("a nil slice or map became non-nil in the clone")
	}
}

// TestProofCloneSharesNothing checks that proof clone shares nothing.
func TestProofCloneSharesNothing(t *testing.T) {
	orig := Proof{
		Identifier: &Identifier{ID: "i1", Value: "+8613800000000"},
		External:   &External{Provider: "google", Subject: "s", Attributes: map[string]string{"idp.dept": "x"}},
		Methods:    []Method{MethodOIDC},
		Aliases:    []ExternalLink{{Provider: "wechat-openid:app", Subject: "o"}},
	}
	c := orig.Clone()

	c.Identifier.Value = "changed"
	c.External.Subject = "changed"
	c.External.Attributes["idp.dept"] = "changed"
	c.Methods[0] = MethodSAML
	c.Aliases[0].Subject = "changed"

	if orig.Identifier.Value != "+8613800000000" {
		t.Error("the clone's Identifier is the original's")
	}
	if orig.External.Subject != "s" || orig.External.Attributes["idp.dept"] != "x" {
		t.Error("the clone's External is the original's")
	}
	if orig.Methods[0] != MethodOIDC {
		t.Error("the clone's Methods is the original's")
	}
	if orig.Aliases[0].Subject != "o" {
		t.Error("the clone's Aliases is the original's")
	}

	if c := (Proof{Account: "a"}).Clone(); c.Identifier != nil || c.External != nil {
		t.Error("cloning a proof with no identifier or external invented one")
	}
}

// TestPassCloneSharesNothing checks that pass clone shares nothing.
func TestPassCloneSharesNothing(t *testing.T) {
	orig := Pass{Methods: []Method{MethodTOTP}, UserVerified: true}
	c := orig.Clone()
	c.Methods[0] = MethodSMS
	if orig.Methods[0] != MethodTOTP {
		t.Error("the clone's Methods is the original's")
	}
	if !c.UserVerified {
		t.Error("the clone lost UserVerified")
	}
}
