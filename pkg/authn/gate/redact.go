package gate

import (
	"strings"

	"github.com/vislake/speed/pkg/log"
)

// credentialKeys are the attribute names a credential would travel under. A
// record written anywhere in the process under one of these is masked.
//
// Each name is registered in both the spellings a call site plausibly uses.
// Key matching is an exact lookup — pkg/log stores what it is given and folds
// nothing — so a name registered in one case leaves the other in the clear, and
// which case a call site picked is not something this module can know.
var credentialKeys = []string{
	authHeader, strings.ToLower(authHeader),
	"Credential", "credential",
}

// registerRedaction registers the credential redaction rules.
//
// The schemes are the ones actually in play: every scheme a backend in this
// assembly claims, plus the one the gateway reads itself. Registering a fixed
// list instead would mask what this build does not use and miss what it does.
//
// A rule only governs judgements made after it is registered, and an attribute
// bound through With is judged once at binding time. This registration lands
// after every module's New, so a logger that took its attributes before then
// keeps them unmasked however late the record is written. That is weaker than
// the timing log offers a module for its own bindings, and weaker again across
// modules, where there is no timing to speak of.
func registerRedaction(r log.Redaction, schemes []string) {
	r.AddKeys(credentialKeys...)
	r.AddPattern(ModuleName+".credential", credentialMatcher(schemes))
}

// credentialMatcher builds the rule that masks the value following an
// authentication scheme wherever one appears inside a string.
//
// The matcher runs on every resolved string value and every error text of every
// record in the process, so its cost lands on the whole logging path. It is kept
// to a scan per scheme for that reason, with no expression engine behind it.
func credentialMatcher(schemes []string) log.Matcher {
	needles := make([]string, 0, len(schemes))
	seen := make(map[string]struct{}, len(schemes))
	for _, scheme := range append([]string{basicScheme}, schemes...) {
		folded := strings.ToLower(strings.TrimSpace(scheme))
		if folded == "" {
			continue
		}
		if _, already := seen[folded]; already {
			continue
		}
		seen[folded] = struct{}{}
		needles = append(needles, folded+" ")
	}

	return func(value string) []log.Span {
		var spans []log.Span
		for _, needle := range needles {
			for from := 0; from < len(value); {
				at := indexFold(value, needle, from)
				if at < 0 {
					break
				}
				from = at + len(needle)
				if !startsWord(value, at) {
					continue
				}
				if span, found := valueSpan(value, from); found {
					spans = append(spans, span)
				}
			}
		}
		// The spans are handed back as found. pkg/log collects the spans
		// of every matcher, drops the out-of-range ones, then sorts and
		// merges the rest before applying them, so sorting here would be
		// the same work done twice — on every string value and every
		// error text of every record in the process.
		return spans
	}
}

// indexFold finds needle in s at or after from, comparing ASCII letters without
// regard to case, and returns an index into s.
//
// The search runs over the original string rather than over a lower-cased copy
// of it. A copy is the obvious way to write this and it is wrong: lower-casing
// is not length-preserving in Unicode — 'İ' is one rune and three bytes, and
// folds to two runes and three bytes, while other runes change length outright —
// so an offset found in the copy indexes the original incorrectly from the first
// such rune onwards. The spans are byte ranges applied to the original, and a
// shifted span masks the wrong bytes and leaves the credential itself readable.
//
// Only ASCII case is folded, which is all this needs: an authentication scheme
// name is a token, and a token is ASCII.
func indexFold(s, needle string, from int) int {
	if len(needle) == 0 {
		return -1
	}
	for at := from; at+len(needle) <= len(s); at++ {
		if equalFoldASCII(s[at:at+len(needle)], needle) {
			return at
		}
	}
	return -1
}

// equalFoldASCII reports whether two equal-length strings match once ASCII
// letters are folded. needle is already lower case.
func equalFoldASCII(s, needle string) bool {
	for i := 0; i < len(needle); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != needle[i] {
			return false
		}
	}
	return true
}

// startsWord reports whether position at begins a word, so that the tail of a
// longer token is not taken for a scheme name.
func startsWord(s string, at int) bool {
	if at == 0 {
		return true
	}
	return !isWordByte(s[at-1])
}

// isWordByte reports whether b can sit inside a scheme name. Scheme names are
// tokens, so this is the ASCII letter and digit set.
func isWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return false
}

// valueSpan takes the credential value starting at from and running to the next
// whitespace or the end of the string.
func valueSpan(value string, from int) (log.Span, bool) {
	end := from
	for end < len(value) && !isSpaceByte(value[end]) {
		end++
	}
	if end == from {
		return log.Span{}, false
	}
	return log.Span{Start: from, End: end}, true
}

// isSpaceByte reports whether b ends a credential value.
func isSpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '"', '\'':
		return true
	}
	return false
}
