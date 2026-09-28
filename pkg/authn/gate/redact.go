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
// What it masks is one word: wherever a scheme name in play begins a word and is
// followed by blanks, the run after the blanks up to the next whitespace or
// quote, unless that run is a word of schemeProse. That covers a credential in
// the token68 form every scheme in this repository uses. A credential written as
// a list of auth-params is masked only up to its first quote or blank, and not
// at all when its first parameter is named by a word of schemeProse (realm=,
// token=); a value that runs across several words keeps all but the first.
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
				span, found := valueSpan(value, from)
				if found && !isProse(value[span.Start:span.End]) {
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

// schemeProse is the vocabulary a scheme name is followed by when it is written
// about rather than sent: the nouns a sentence about authentication puts after
// "Basic" or "Bearer", and the function words that follow a scheme name standing
// in a sentence of its own.
//
// Leaving a word of this list unmasked gives nothing away, whatever the scheme
// and whatever the writer meant by it. The list is fixed and public, so a
// credential that is one of its words is a credential anybody reading this file
// can name. That is what makes an exemption by list safe where one by shape is
// not: no shape tells the word "token" from a bearer token that happens to be
// five lower-case letters, and the Basic credential of k:s is "azpz".
//
// Every other word after a scheme name is masked, prose included. The list keeps
// the sentences this repository and its hosts write about credentials readable;
// it does not make every English word after a scheme name survive.
var schemeProse = map[string]struct{}{
	// What a sentence about a credential names after the scheme.
	"auth": {}, "authentication": {}, "authorization": {},
	"challenge": {}, "challenges": {}, "credential": {}, "credentials": {},
	"header": {}, "headers": {}, "realm": {}, "scheme": {}, "schemes": {},
	"token": {}, "tokens": {},
	// What follows a scheme name that is itself the subject or object of a
	// sentence.
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"but": {}, "by": {}, "for": {}, "from": {}, "has": {}, "if": {}, "in": {},
	"is": {}, "it": {}, "nor": {}, "not": {}, "of": {}, "on": {}, "or": {},
	"than": {}, "that": {}, "the": {}, "then": {}, "to": {}, "was": {},
	"were": {}, "when": {}, "which": {}, "with": {},
}

// isProse reports whether the run after a scheme name is a word of schemeProse,
// in any case and with the punctuation a sentence puts after a word — or the
// '=' a challenge puts after "realm" — left off.
func isProse(run string) bool {
	_, listed := schemeProse[strings.ToLower(strings.TrimRight(run, ".,;:!?)="))]
	return listed
}

// valueSpan takes the credential value that follows a scheme name: from the
// first byte after the blanks that separate the two, to the next whitespace or
// quote or the end of the string.
//
// The needle carries one space, and RFC 9110 separates scheme and credential by
// one or more; the gateway trims whatever surrounds the value, so a credential
// written with several blanks is one it would have read.
func valueSpan(value string, from int) (log.Span, bool) {
	for from < len(value) && (value[from] == ' ' || value[from] == '\t') {
		from++
	}
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
