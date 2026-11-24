package gate

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/log"
)

// TestShapeRulesMaskEveryOccurrenceInAValue checks that shape rules mask
// every occurrence in a value.
func TestShapeRulesMaskEveryOccurrenceInAValue(t *testing.T) {
	const jwtTwo = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJib2IifQ.YW5vdGhlcnNpZw"
	for _, c := range []struct {
		name    string
		value   string
		secrets []string
		kept    []string
	}{
		{"two JWTs", "first " + jwtToken + " then " + jwtTwo + " end", []string{jwtToken, jwtTwo}, []string{"first", "then", "end"}},
		{
			"two refresh tokens", "old spd_rt_AAAAAAAAAA new spd_rt_BBBBBBBBBB end",
			[]string{"spd_rt_AAAAAAAAAA", "spd_rt_BBBBBBBBBB"},
			[]string{"old", "new", "end"},
		},
		{
			"a refresh token and a client secret", "x " + refreshToken + " y " + clientSecret + " z",
			[]string{refreshToken, clientSecret},
			[]string{"x", "y", "z"},
		},
		{
			"a JWT after a candidate that is the middle of a longer token", "ZXlKeyJhbGciOiJFUzI1NiJ9.e30.c2ln then " + jwtToken,
			[]string{jwtToken},
			[]string{"then"},
		},
		{"two secret pairs", "?secret=one&x=1&password=two&y=2", []string{"one", "two"}, []string{"x=1", "y=2"}},
		{
			"two otpauth addresses", "a otpauth://totp/Speed:alice?issuer=Speed b OTPAUTH://totp/Speed:bob?issuer=Speed c",
			[]string{"alice", "bob"},
			[]string{"a ", " b ", " c"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := mask(shapeMatcher, c.value)
			for _, secret := range c.secrets {
				if strings.Contains(got, secret) {
					t.Errorf("the masked value %q still carries %q", got, secret)
				}
			}
			for _, kept := range c.kept {
				if !strings.Contains(got, kept) {
					t.Errorf("the masked value %q lost %q, which is not a secret", got, kept)
				}
			}
			if n := strings.Count(got, log.MaskedText); n < len(c.secrets) {
				t.Errorf("the masked value %q carries %d mask(s), want at least %d", got, n, len(c.secrets))
			}
		})
	}
}

// An otpauth address is masked whole: its label names the account the seed
// belongs to and its query carries the seed, and neither is a key=value pair the
// pair rule would catch on its own.
func TestAnOtpauthAddressIsMaskedWholeWithoutHelpFromThePairRule(t *testing.T) {
	got := mask(shapeMatcher, "enrolled otpauth://totp/Speed:alice?issuer=Speed&digits=6 ok")
	for _, leaked := range []string{"alice", "issuer", "digits"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the masked value %q still carries %q", got, leaked)
		}
	}
	if !strings.Contains(got, "enrolled") || !strings.Contains(got, "ok") {
		t.Errorf("the masked value %q lost the words around the address", got)
	}
}

// eyJ glued to one byte before it is the middle of a longer token, the same as
// when it is glued to four.
func TestAJWTGluedToOneByteBeforeItIsLeftAlone(t *testing.T) {
	value := "x" + jwtToken
	if got := mask(shapeMatcher, value); got != value {
		t.Errorf("the value reads %q once masked, want it unchanged", got)
	}
}

// A scheme name found inside a longer word is not a scheme, and finding one does
// not end the scan: the real one may follow it in the same value.
func TestASchemeInsideAWordDoesNotEndTheScan(t *testing.T) {
	got := mask(credentialMatcher([]string{"basic"}), "xbasic abc then Basic SECRETVALUE end")
	if strings.Contains(got, "SECRETVALUE") {
		t.Errorf("the masked value %q still carries the credential that followed a false start", got)
	}
	if !strings.Contains(got, "abc") {
		t.Errorf("the masked value %q lost the word after the false start", got)
	}
}

// TestSchemeMatcherHoldsItsEdges checks that scheme matcher holds its edges.
func TestSchemeMatcherHoldsItsEdges(t *testing.T) {
	for _, c := range []struct {
		name    string
		schemes []string
		value   string
		secret  string // empty: the value has to come back unchanged
	}{
		{"the first letter of the scheme has to match", []string{"basic"}, "xasic abc", ""},
		{"a digit before the scheme is part of a word", []string{"basic"}, "0basic abc", ""},
		{"another digit before the scheme is part of a word", []string{"basic"}, "9basic abc", ""},
		{"a Z in the scheme is folded", []string{"zeta"}, "ZETA secret1", "secret1"},
		{"a duplicate scheme does not drop the ones after it", []string{"basic", "basic", "bearer"}, "Bearer secret2", "secret2"},
		{"a blank scheme does not drop the ones after it", []string{"", "bearer"}, "Bearer secret3", "secret3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := mask(credentialMatcher(c.schemes), c.value)
			if c.secret == "" {
				if got != c.value {
					t.Errorf("the value reads %q once masked, want it unchanged", got)
				}
				return
			}
			if strings.Contains(got, c.secret) {
				t.Errorf("the masked value %q still carries %q", got, c.secret)
			}
		})
	}
}

// What begins a value is a place a secret can be too.
func TestShapeRulesFindASecretAtTheStartOfAValue(t *testing.T) {
	if got := mask(shapeMatcher, "otpauth://totp/Speed:alice?issuer=Speed"); strings.Contains(got, "alice") {
		t.Errorf("an otpauth address at the start of the value reads %q", got)
	}
	if got := mask(shapeMatcher, "=x&secret=hunter2"); strings.Contains(got, "hunter2") {
		t.Errorf("a secret pair after a leading equals sign reads %q", got)
	}
}

// Two segments and two trailing dots read as three segments with an empty last
// one once a single dot is taken off, which is not a JWT.
func TestAnEmptyLastSegmentIsNotAJWT(t *testing.T) {
	value := "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.."
	if got := mask(shapeMatcher, value); got != value {
		t.Errorf("the value reads %q once masked, want it unchanged", got)
	}
}

// TestShapeSpansCoverExactlyTheSecret holds that the spans a shape rule returns cover exactly the secret, and that the characters of the base64url alphabet stay inside it.
func TestShapeSpansCoverExactlyTheSecret(t *testing.T) {
	jwtDashHeader := "eyJhbGci-OiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2lnbmF0dXJl"
	jwtUnderPayload := "eyJhbGciOiJFUzI1NiJ9.eyJzdWIi_OiJhbGljZSJ9.c2lnbmF0dXJl"
	jwtDashSig := "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2lnbmF0-dXJl_x"
	M := log.MaskedText
	for _, c := range []struct{ name, in, want string }{
		{"jwt exact", "the token " + jwtToken + " was refused", "the token " + M + " was refused"},
		{"jwt at the end", "refused " + jwtToken, "refused " + M},
		{"jwt dash in header", "x " + jwtDashHeader + " y", "x " + M + " y"},
		{"jwt underscore in payload", "x " + jwtUnderPayload + " y", "x " + M + " y"},
		{"jwt dash in signature", "x " + jwtDashSig + " y", "x " + M + " y"},
		{"refresh token exact", "rotating " + refreshToken + " now", "rotating " + M + " now"},
		{"refresh token with dash and underscore", "rotating spd_rt_ab-cd_ef-gh now", "rotating " + M + " now"},
		{"refresh token before a full stop", "rotated spd_rt_abc123.", "rotated " + M + "."},
		{"client secret exact", "key " + clientSecret + ", rest", "key " + M + ", rest"},
		{"otpauth whole address", "enrolled otpauth://totp/Speed:alice@example.com?algorithm=SHA1&secret=JBSWY3DP&issuer=Speed ok", "enrolled " + M + " ok"},
		{"otpauth upper case", "enrolled OTPAUTH://totp/Speed:alice@example.com?secret=JBSWY3DP ok", "enrolled " + M + " ok"},
		{"otpauth without a secret key", "enrolled otpauth://totp/Speed:alice@example.com?issuer=Speed ok", "enrolled " + M + " ok"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mask(shapeMatcher, c.in); got != c.want {
				t.Errorf("masked to %q, want %q", got, c.want)
			}
		})
	}
}

// TestExactMasksForPairsTerminatorsAndProse holds the exact masks of the pair rules, the terminators of a value, and the punctuation that follows a word of prose.
func TestExactMasksForPairsTerminatorsAndProse(t *testing.T) {
	M := log.MaskedText
	for _, c := range []struct{ name, in, want string }{
		{"query pair", "GET /cb?secret=hunter2&x=1", "GET /cb?secret=" + M + "&x=1"},
		{"cookie pair without a blank after the semicolon", "Cookie: authn-sid=abc;theme=dark", "Cookie: authn-sid=" + M + ";theme=dark"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mask(shapeMatcher, c.in); got != c.want {
				t.Errorf("masked to %q, want %q", got, c.want)
			}
		})
	}
	matcher := credentialMatcher([]string{"basic", "bearer", "zoom"})
	for _, c := range []struct{ name, in, want string }{
		{"newline ends the value", "Basic abc123\nnext line", "Basic " + M + "\nnext line"},
		{"carriage return ends the value", "Basic abc123\rnext", "Basic " + M + "\rnext"},
		{"tab ends the value", "Basic abc123\tnext", "Basic " + M + "\tnext"},
		{"single quote ends the value", "'Basic abc123' next", "'Basic " + M + "' next"},
		{"double quote ends the value", `"Basic abc123" next`, `"Basic ` + M + `" next`},
		{"a scheme with a z in it", "ZOOM abc123 next", "ZOOM " + M + " next"},
		{"prose then semicolon", "see Basic header; next", "see Basic header; next"},
		{"prose then colon", "see Bearer token: next", "see Bearer token: next"},
		{"prose then exclamation mark", "no Basic auth! next", "no Basic auth! next"},
		{"prose then question mark", "is it Basic auth? next", "is it Basic auth? next"},
		{"prose then closing parenthesis", "(see Basic header) next", "(see Basic header) next"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mask(matcher, c.in); got != c.want {
				t.Errorf("masked to %q, want %q", got, c.want)
			}
		})
	}
}
