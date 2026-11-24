package authn

import (
	"strconv"
	"testing"
)

// TestMethodTexts holds the text each method carries: the amr claim of a
// token and the method list of a session row.
func TestMethodTexts(t *testing.T) {
	for _, tc := range []struct {
		got  Method
		want string
	}{
		{MethodPassword, "pwd"},
		{MethodSMS, "sms"},
		{MethodTOTP, "otp"},
		{MethodWebAuthn, "webauthn"},
		{MethodOAuth, "oauth"},
		{MethodOIDC, "oidc"},
		{MethodSAML, "saml"},
		{MethodStatic, "static"},
		{MethodClientSecret, "client-secret"},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("method constant reads %q, want %q", tc.got, tc.want)
		}
		if !tc.got.Valid() {
			t.Errorf("method %q is not in the closed set", tc.got)
		}
	}
}

// TestAssuranceNumbers holds the numbers the acr claim of a token carries as
// decimal text.
func TestAssuranceNumbers(t *testing.T) {
	for _, tc := range []struct {
		got  Assurance
		want string
	}{
		{AssuranceNone, "0"},
		{AssuranceSingle, "1"},
		{AssuranceMulti, "2"},
	} {
		if got := strconv.Itoa(int(tc.got)); got != tc.want {
			t.Errorf("assurance level reads %s, want %s", got, tc.want)
		}
	}
}

// TestAccessLevelsAreDistinct holds the four levels apart and the zero value
// at AccessUnknown, the reading of a request no gateway decided about.
func TestAccessLevelsAreDistinct(t *testing.T) {
	levels := map[string]Access{
		"AccessUnknown":  AccessUnknown,
		"AccessOpen":     AccessOpen,
		"AccessOptional": AccessOptional,
		"AccessRequired": AccessRequired,
	}
	if AccessUnknown != 0 {
		t.Errorf("AccessUnknown is %d, want the zero value", AccessUnknown)
	}
	seen := map[Access]string{}
	for name, level := range levels {
		if other, dup := seen[level]; dup {
			t.Errorf("%s and %s are the same level, %d", name, other, level)
		}
		seen[level] = name
	}
	if len(seen) != 4 {
		t.Errorf("%d distinct access levels, want 4", len(seen))
	}
}

// TestFactorTexts holds the kind texts a factor row stores.
func TestFactorTexts(t *testing.T) {
	for _, tc := range []struct {
		got  FactorKind
		want string
	}{
		{FactorTOTP, "totp"},
		{FactorSMS, "sms"},
		{FactorWebAuthn, "webauthn"},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("factor kind reads %q, want %q", tc.got, tc.want)
		}
	}
}

// TestFactorStatusesAreDistinct holds the three statuses apart and the zero
// value at FactorPending, which verification does not use.
func TestFactorStatusesAreDistinct(t *testing.T) {
	if FactorPending != 0 {
		t.Errorf("FactorPending is %d, want the zero value", FactorPending)
	}
	if FactorActive == FactorPending || FactorDisabled == FactorPending || FactorActive == FactorDisabled {
		t.Error("two factor statuses are the same value")
	}
}

// TestIdentifierTexts holds the kind texts an identifier row stores.
func TestIdentifierTexts(t *testing.T) {
	for _, tc := range []struct {
		got  IdentifierKind
		want string
	}{
		{IdentifierUsername, "username"},
		{IdentifierEmail, "email"},
		{IdentifierPhone, "phone"},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("identifier kind reads %q, want %q", tc.got, tc.want)
		}
	}
}

// TestFieldKindTexts holds the kind texts a client reads from a login method's
// description of its inputs.
func TestFieldKindTexts(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{FieldText, "text"},
		{FieldPassword, "password"},
		{FieldCode, "code"},
		{FieldPhone, "phone"},
	} {
		if tc.got != tc.want {
			t.Errorf("field kind reads %q, want %q", tc.got, tc.want)
		}
	}
}
