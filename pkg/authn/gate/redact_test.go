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
	matcher := credentialMatcher([]string{"bearer"})

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
	matcher := credentialMatcher([]string{"bearer"})

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
	matcher := credentialMatcher(nil)
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
	matcher := credentialMatcher([]string{"bearer"})

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
// the one the gateway reads itself always is.
func TestTheSchemesMaskedAreTheOnesInPlay(t *testing.T) {
	const secret = "ey.Jh.bGci"

	if got := mask(credentialMatcher(nil), "Bearer "+secret); !strings.Contains(got, secret) {
		t.Errorf("a scheme no backend claims was masked (%q); the rule runs on every string "+
			"value in the process, so it covers what this assembly carries and no more", got)
	}
	if got := mask(credentialMatcher([]string{"bearer"}), "Bearer "+secret); strings.Contains(got, secret) {
		t.Errorf("a claimed scheme was not masked (%q)", got)
	}
	if got := mask(credentialMatcher([]string{"bearer"}), "Basic "+secret); strings.Contains(got, secret) {
		t.Errorf("the scheme the gateway reads itself was not masked (%q)", got)
	}
}
