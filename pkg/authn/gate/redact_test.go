package gate

import (
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/log"
)

// mask applies the spans the matcher reports, the way the logging layer does —
// ordered by start and with overlapping ones merged, since the matcher hands
// them back in the order it found them — so that a case reads what would
// actually reach a destination.
func mask(matcher log.Matcher, value string) string {
	spans := slices.Clone(matcher(value))
	slices.SortFunc(spans, func(a, b log.Span) int { return a.Start - b.Start })
	var b strings.Builder
	at, masked := 0, false
	for _, s := range spans {
		if masked && s.Start <= at {
			// Overlapping or touching the span before it, so it is
			// part of the one mask already written.
			at = max(at, s.End)
			continue
		}
		b.WriteString(value[at:s.Start])
		b.WriteString(log.MaskedText)
		at, masked = s.End, true
	}
	b.WriteString(value[at:])
	return b.String()
}

// TestTheCredentialIsMaskedWhereverTheSchemeAppears holds the ordinary cases.
func TestTheCredentialIsMaskedWhereverTheSchemeAppears(t *testing.T) {
	matcher := credentialMatcher([]string{"basic", "bearer"})

	for _, c := range []struct {
		name   string
		value  string
		secret string
		kept   string
	}{
		{
			name:   "a Basic credential",
			value:  "Basic YWxpY2U6czNjcmV0",
			secret: "YWxpY2U6czNjcmV0",
		},
		{
			name:   "a scheme a backend claimed",
			value:  "Bearer ey.Jh.bGci",
			secret: "ey.Jh.bGci",
		},
		{
			name:   "the scheme written in another case",
			value:  "bAsIc YWxpY2U6czNjcmV0",
			secret: "YWxpY2U6czNjcmV0",
		},
		{
			name:   "a credential inside a longer sentence",
			value:  "rejected the header Basic YWxpY2U6czNjcmV0 from 10.0.0.1",
			secret: "YWxpY2U6czNjcmV0",
			kept:   "10.0.0.1",
		},
		{
			name:  "a word merely ending in the scheme name",
			value: "notbasic YWxpY2U6czNjcmV0",
			kept:  "YWxpY2U6czNjcmV0",
		},
		{
			// RFC 9110 separates the scheme from the credential with
			// one or more spaces, and the gateway reads a credential
			// written with several, so a string carrying one that way
			// carries a credential the gateway would have accepted.
			name:   "a credential after more than one space",
			value:  "Basic   YWxpY2U6czNjcmV0",
			secret: "YWxpY2U6czNjcmV0",
		},
		{
			name:   "a credential after a tab",
			value:  "rejected Bearer \tey.Jh.bGci",
			secret: "ey.Jh.bGci",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := mask(matcher, c.value)
			if c.secret != "" && strings.Contains(got, c.secret) {
				t.Errorf("the masked value %q still carries the credential %q", got, c.secret)
			}
			if c.kept != "" && !strings.Contains(got, c.kept) {
				t.Errorf("the masked value %q lost %q, which is not a credential", got, c.kept)
			}
		})
	}
}

// TestProseAboutASchemeKeepsItsWords holds the other side of a rule that runs on
// every string of every record in the process.
//
// A scheme name is also an English word, and the text written about a credential
// uses it as one: a backend's refusal cause, the gateway's own sentences, any
// other module's. Taking the word after it for a credential masks ordinary prose
// — "the bearer [REDACTED] names no live session" — and a refusal cause masked
// that way does not say what was wrong. Every value here is text a real
// component writes or would write, and none of it carries a credential, so each
// has to reach the destination exactly as it was written.
func TestProseAboutASchemeKeepsItsWords(t *testing.T) {
	matcher := credentialMatcher([]string{"basic", "bearer"})

	for _, value := range []string{
		// pkg/authn/static's refusal of a malformed Basic credential.
		"authn.static: the Basic credential is not base64 of user:password, or it is " +
			"qualified with a backend that is not this one",
		"the bearer token names no live session",
		"Basic auth is switched off on this endpoint",
		"neither Basic nor Bearer is claimed by any backend",
		"refused: the Bearer Token.",
		"the challenge read Basic realm=\"public\"",
		"the bearer credentials, the basic scheme and the Bearer header",
	} {
		t.Run(value, func(t *testing.T) {
			if got := mask(matcher, value); got != value {
				t.Errorf("prose about a scheme reads %q once masked, want it unchanged: the "+
					"word after the scheme name is a word, not a credential", got)
			}
		})
	}
}

// TestACaseChangingRuneDoesNotShiftTheMask holds the one thing that makes this
// matcher hard to write.
//
// Searching a lower-cased copy and applying the offsets to the original is the
// obvious implementation and it is wrong: lower-casing is not length-preserving
// in Unicode. 'İ' is two bytes and folds to three, so every offset past it is
// short by one, and the mask lands one byte early — covering the last byte of
// the scheme name and leaving the first byte of the credential in the clear.
//
// A record that reaches a destination with a credential in it cannot be taken
// back, and nothing downstream would report that it happened.
func TestACaseChangingRuneDoesNotShiftTheMask(t *testing.T) {
	matcher := credentialMatcher([]string{"basic"})
	const secret = "YWxpY2U6czNjcmV0"

	for _, prefix := range []string{
		"",
		"İ ",      // two bytes, folds to three
		"İİİ ",    // three of them, so the shift is past one byte
		"ΣΣ ",     // Greek capitals, folding to a different byte length
		"straße ", // already lower case, left alone
	} {
		t.Run("prefixed with "+prefix, func(t *testing.T) {
			got := mask(matcher, prefix+"Basic "+secret)
			if strings.Contains(got, secret) {
				t.Errorf("the masked value %q carries the credential in the clear; a rune "+
					"before it changed length when folded and the mask moved with it", got)
			}
			if !strings.Contains(got, log.MaskedText) {
				t.Errorf("the masked value %q carries no mask at all", got)
			}
		})
	}
}

// TestEverySpanHandedOverIsUsable holds the shape of what this matcher returns.
//
// pkg/log collects the spans of every matcher, drops any that are empty or
// reach outside the value, then sorts and merges the rest. Dropping is silent,
// so a span this matcher gets wrong fails nowhere — it just leaves a credential
// unmasked. Ordering and overlap are the logging layer's to settle and are
// deliberately not repeated on this side; what has to hold here is that every
// span covers something and lands inside the string it was found in.
func TestEverySpanHandedOverIsUsable(t *testing.T) {
	matcher := credentialMatcher([]string{"basic", "bearer"})

	for _, value := range []string{
		"Basic YWxpY2U6czNjcmV0",
		"Basic YWxpY2U6czNjcmV0 and Basic Ym9iOnMzY3JldA",
		"Bearer ey.Jh.bGci then Basic YWxpY2U6czNjcmV0",
		"basic",
		"Basic ",
		"",
	} {
		t.Run(value, func(t *testing.T) {
			for i, span := range matcher(value) {
				if span.Start < 0 || span.End > len(value) {
					t.Errorf("span %d is %v, outside a value of %d byte(s); the logging layer "+
						"drops it without a word and the credential stays readable",
						i, span, len(value))
				}
				if span.End <= span.Start {
					t.Errorf("span %d is %v and covers nothing, so it is dropped silently",
						i, span)
				}
			}
		})
	}
}

// TestTheSchemesMaskedAreTheOnesInPlay holds that the rule follows the assembly
// rather than a fixed list: a scheme no backend claims is not searched for, and
// every scheme one does claim is, Basic included. The gateway interprets no
// credential, so it has no scheme of its own to add.
func TestTheSchemesMaskedAreTheOnesInPlay(t *testing.T) {
	const secret = "ey.Jh.bGci"

	for _, scheme := range []string{"Bearer", "Basic"} {
		if got := mask(credentialMatcher(nil), scheme+" "+secret); !strings.Contains(got, secret) {
			t.Errorf("%s was masked with no backend claiming it (%q); the rule runs on every string "+
				"value in the process, so it covers what this assembly carries and no more", scheme, got)
		}
	}
	if got := mask(credentialMatcher([]string{"bearer"}), "Bearer "+secret); strings.Contains(got, secret) {
		t.Errorf("a claimed scheme was not masked (%q)", got)
	}
	if got := mask(credentialMatcher([]string{"bearer"}), "Basic "+secret); !strings.Contains(got, secret) {
		t.Errorf("a scheme no backend claims was masked because another is (%q)", got)
	}
	if got := mask(credentialMatcher([]string{"basic"}), "Basic "+secret); strings.Contains(got, secret) {
		t.Errorf("Basic was not masked though a backend claims it (%q)", got)
	}
}

// secretsOf are the values of each shape the matcher has to find, written the way
// they would reach a log line.
const (
	jwtToken     = "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2lnbmF0dXJl"
	refreshToken = "spd_rt_4fK9xQ2mVb7LzP0aRtYw"
	clientSecret = "spd_cs_Zk3QpL8vN1cXyWe2"
)

// TestEveryShapeOfSecretIsMaskedWhereverItIsWritten holds one value of each shape
// the design lists, under no key at all: the rule is what is left when nobody
// registered a key for a value that was logged under a name nobody thought of.
func TestEveryShapeOfSecretIsMaskedWhereverItIsWritten(t *testing.T) {
	for _, c := range []struct {
		name   string
		value  string
		secret string
		kept   string
	}{
		{"a JWT", "the token " + jwtToken + " was refused", jwtToken, "was refused"},
		{"a JWT at the start of the value", jwtToken, jwtToken, ""},
		{"a JWT before a full stop", "refused " + jwtToken + ".", jwtToken, "refused"},
		{"a JWT inside quotes", `header="` + jwtToken + `" end`, jwtToken, "end"},
		{"a refresh token", "rotating " + refreshToken + " now", refreshToken, "rotating"},
		{"a client secret", "key " + clientSecret + ", rest", clientSecret, "rest"},
		{"an otpauth address", "enrolled otpauth://totp/Speed:alice?secret=JBSWY3DPEHPK3PXP&issuer=Speed ok", "JBSWY3DPEHPK3PXP", "ok"},
		{"an otpauth address in another case", "OTPAUTH://totp/Speed:alice?secret=JBSWY3DPEHPK3PXP", "JBSWY3DPEHPK3PXP", ""},
		{"a secret in a query string", "GET /cb?secret=hunter2&x=1", "hunter2", "x=1"},
		{"a code in a query string", "GET /cb?state=s&code=4/0AbCd", "4/0AbCd", "state=s"},
		{"an access token in a query string", "url ?access_token=at-123 done", "at-123", "done"},
		{"a refresh token in a query string", "?refresh_token=rt-123", "rt-123", ""},
		{"an id token in a query string", "?id_token=it-123&a=b", "it-123", "a=b"},
		{"a client secret in a query string", "?client_secret=cs-123", "cs-123", ""},
		{"a SAML response in a query string", "?SAMLResponse=PHNhbWw%2B&RelayState=x", "PHNhbWw%2B", "RelayState=x"},
		{"an assertion in a query string", "?assertion=as-123", "as-123", ""},
		{"a password in a query string", "?password=p4ss&u=alice", "p4ss", "u=alice"},
		{"a query pair in a wechat url", "Get \"https://api.weixin.qq.com/sns/oauth2/access_token?appid=wx1&secret=wxsecret-9&code=c1&grant_type=authorization_code\": dial tcp: lookup failed", "wxsecret-9", "appid=wx1"},
		{"the session cookie", "Cookie: sid=session-secret-1; theme=dark", "session-secret-1", "theme=dark"},
		{"the host-prefixed session cookie", "Cookie: __Host-sid=session-secret-2; theme=dark", "session-secret-2", "theme=dark"},
		{"the secure-prefixed session cookie", "Set-Cookie: __Secure-sid=session-secret-3; Path=/", "session-secret-3", "Path=/"},
		{"the session cookie in another case", "SID=session-secret-4", "session-secret-4", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := mask(shapeMatcher, c.value)
			if strings.Contains(got, c.secret) {
				t.Errorf("the masked value %q still carries %q", got, c.secret)
			}
			if !strings.Contains(got, log.MaskedText) {
				t.Errorf("the masked value %q carries no mask at all", got)
			}
			if c.kept != "" && !strings.Contains(got, c.kept) {
				t.Errorf("the masked value %q lost %q, which is not a secret", got, c.kept)
			}
		})
	}
}

// TestAWordThatOnlyResemblesASecretShapeIsLeftAlone holds the other side of a rule
// that runs on every string of every record in the process: each value here is
// text a component writes about something that is not a secret, and has to reach
// its destination as written.
func TestAWordThatOnlyResemblesASecretShapeIsLeftAlone(t *testing.T) {
	for _, c := range []struct {
		name  string
		value string
	}{
		{"a word that begins with a secret key", "secretary=Jane"},
		{"a word that ends with a secret key", "barcode=123 zipcode=94105"},
		{"a key that contains a secret key after an underscore", "my_secret=abc"},
		{"a key that is no secret", "user=alice&page=2"},
		{"an equals sign with no key", "a = b and =c"},
		{"a secret key with no value", "?secret=&x=1"},
		{"a cookie that is not the session's", "Cookie: theme=dark; side=left"},
		{"two JWT segments", "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9"},
		{"two JWT segments before a full stop", "refused eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9."},
		{"four dotted segments", "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2ln.bmF0"},
		{"eyJ in the middle of a longer token", "ZXlKeyJhbGciOiJFUzI1NiJ9.e30.c2ln"},
		{"an empty segment", "eyJhbGciOiJFUzI1NiJ9..c2ln"},
		{"a token prefix with nothing after it", "the spd_rt_ prefix and the spd_cs_ prefix"},
		{"the otpauth scheme with no address", "an otpauth:// address"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mask(shapeMatcher, c.value); got != c.value {
				t.Errorf("the value reads %q once masked, want it unchanged", got)
			}
		})
	}
}

// TestAJWTNeedsExactlyThreeSegments holds the one place the JWT rule draws a line
// by count: the three segments of a JWS are a token, and two are not.
func TestAJWTNeedsExactlyThreeSegments(t *testing.T) {
	three := "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2lnbmF0dXJl"
	two := "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9"
	if got := mask(shapeMatcher, three); got == three {
		t.Error("three segments were not masked")
	}
	if got := mask(shapeMatcher, two); got != two {
		t.Errorf("two segments were masked (%q), which is not the shape of a JWT", got)
	}
}

// TestEverySpanTheShapeRuleHandsOverIsUsable holds the shape of what the matcher
// returns, for the reason the scheme rule's test gives: pkg/log drops an unusable
// span without a word, so a wrong one fails nowhere and leaves a secret readable.
func TestEverySpanTheShapeRuleHandsOverIsUsable(t *testing.T) {
	for _, value := range []string{
		jwtToken, refreshToken, "otpauth://totp/x?secret=A", "?secret=a&code=b", "sid=abc",
		"x" + jwtToken + "." + refreshToken, "=", "==", "secret=", "sid=;", "eyJ", "eyJ.", "eyJ..",
		"spd_rt_", "otpauth://", "",
	} {
		t.Run(value, func(t *testing.T) {
			for i, span := range shapeMatcher(value) {
				if span.Start < 0 || span.End > len(value) || span.End <= span.Start {
					t.Errorf("span %d is %v in a value of %d byte(s), which the logging layer drops silently",
						i, span, len(value))
				}
			}
		})
	}
}

// TestTheKeyNamesAreEveryNameInEveryCaseTheDesignLists holds the key set: the
// whole of the design's table, each name in the case it is written in, in lower
// case and capitalised, since key matching is exact.
func TestTheKeyNamesAreEveryNameInEveryCaseTheDesignLists(t *testing.T) {
	spellings := keySpellings(credentialKeyNames)
	for _, name := range []string{
		"Authorization", "Cookie", "Set-Cookie", "Credential", "password", "new_password",
		"current_password", "code", "otp", "secret", "client_secret", "refresh_token",
		"access_token", "id_token", "token", "assertion", "SAMLResponse", "state",
		"code_verifier", "flow_token", "credential",
	} {
		for _, spelling := range []string{name, strings.ToLower(name), strings.ToUpper(name[:1]) + strings.ToLower(name[1:])} {
			if !slices.Contains(spellings, spelling) {
				t.Errorf("the key %q is not registered, and key matching is exact", spelling)
			}
		}
	}
	if len(spellings) != len(slices.Compact(slices.Sorted(slices.Values(spellings)))) {
		t.Error("a spelling is registered twice")
	}
}
