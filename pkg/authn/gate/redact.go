package gate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// credentialKeyNames are the attribute names a credential or a one-time secret
// would travel under; a record written anywhere in the process under one is masked.
var credentialKeyNames = []string{
	"Authorization", "Cookie", "Set-Cookie", "Credential",
	"password", "new_password", "current_password",
	"code", "otp", "secret", "client_secret",
	"refresh_token", "access_token", "id_token", "token",
	"assertion", "SAMLResponse", "state", "code_verifier", "flow_token",
	"credential",
}

// keySpellings lists every spelling registered for the names: as written, in lower
// case, and with its first letter capitalised. Key matching is an exact lookup, so
// a name registered in one case leaves the others in the clear.
func keySpellings(names []string) []string {
	spellings := make([]string, 0, 3*len(names))
	seen := make(map[string]struct{}, 3*len(names))
	add := func(name string) {
		if _, already := seen[name]; already {
			return
		}
		seen[name] = struct{}{}
		spellings = append(spellings, name)
	}
	for _, name := range names {
		lower := strings.ToLower(name)
		add(name)
		add(lower)
		add(strings.ToUpper(lower[:1]) + lower[1:])
	}
	return spellings
}

// registerNewRules registers the credential key names and the value-shape rule with
// the logging capability when the assembly carries one, in New, before any logger
// binds an attribute. Absence is legal; any other failure is carried out.
func registerNewRules(reg *core.Registry) error {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil
		}
		return fmt.Errorf("%staking up the logging capability to register the credential key "+
			"names with: %w", diagnosticPrefix, err)
	}
	rules := delivered.Redaction()
	rules.AddKeys(keySpellings(credentialKeyNames)...)
	rules.AddPattern(ModuleName+".shapes", shapeMatcher)
	return nil
}

// registerValueRule registers the rule that masks the value following an
// authentication scheme, for every scheme a backend in this assembly takes. It runs
// in Init, where the schemes are read from the backends' declarations.
func registerValueRule(r log.Redaction, schemes []string) {
	r.AddPattern(ModuleName+".credential", credentialMatcher(schemes))
}

// secretPairKeys are the lower-case keys of a key=value query-string pair whose
// value is a secret. A key is compared whole: secretary or barcode is not a match.
var secretPairKeys = map[string]struct{}{
	"secret": {}, "code": {}, "access_token": {}, "refresh_token": {},
	"id_token": {}, "client_secret": {}, "samlresponse": {}, "assertion": {},
	"password": {},
}

// sessionCookieKeys are the session cookie's names in lower case, every form
// cookiename.Name gives the session stem.
var sessionCookieKeys = map[string]struct{}{
	strings.ToLower(cookiename.Session):                           {},
	strings.ToLower(cookiename.HostPrefix + cookiename.Session):   {},
	strings.ToLower(cookiename.SecurePrefix + cookiename.Session): {},
}

// longestPairKey bounds the key a pair is looked up under; it is the length of the
// longest key of the two sets.
var longestPairKey = longestKey(secretPairKeys, sessionCookieKeys)

func longestKey(sets ...map[string]struct{}) int {
	n := 0
	for _, set := range sets {
		for key := range set {
			n = max(n, len(key))
		}
	}
	return n
}

var tokenPrefixes = []string{"spd_rt_", "spd_cs_", "spd_inv_"}

// shapeMatcher masks the values whose shape says they are secrets whatever they
// were logged under: a JWT, a token with one of this capability's prefixes, an
// otpauth:// address, and a secret pair's value in a query string or cookie.
func shapeMatcher(value string) []log.Span {
	var spans []log.Span
	spans = appendJWTSpans(spans, value)
	for _, prefix := range tokenPrefixes {
		spans = appendTokenSpans(spans, value, prefix)
	}
	spans = appendURISpans(spans, value, "otpauth://")
	return appendPairSpans(spans, value)
}

// appendJWTSpans masks each run shaped like a compact JWT: three non-empty
// base64url segments joined by dots, the first beginning with the encoding of
// '{"'. A run of two segments, or of four or more, is some other dotted token.
func appendJWTSpans(spans []log.Span, value string) []log.Span {
	for from := 0; from < len(value); {
		at := strings.Index(value[from:], "eyJ")
		if at < 0 {
			break
		}
		at += from
		end := at
		for end < len(value) && isJWTByte(value[end]) {
			end++
		}
		from = end
		if at > 0 && isJWTByte(value[at-1]) {
			// The middle of a longer token, not the start of one.
			continue
		}
		run := strings.TrimSuffix(value[at:end], ".")
		segments := strings.Split(run, ".")
		if len(segments) == 3 && segments[0] != "" && segments[1] != "" && segments[2] != "" {
			spans = append(spans, log.Span{Start: at, End: at + len(run)})
		}
	}
	return spans
}

func isJWTByte(b byte) bool { return b == '.' || isKeyByte(b) }

// appendTokenSpans masks each token that begins with prefix, to the end of the run
// of URL-safe characters after it; a bare prefix is a word, not a token.
func appendTokenSpans(spans []log.Span, value, prefix string) []log.Span {
	for from := 0; from < len(value); {
		at := strings.Index(value[from:], prefix)
		if at < 0 {
			break
		}
		at += from
		end := at + len(prefix)
		for end < len(value) && isKeyByte(value[end]) {
			end++
		}
		if end > at+len(prefix) {
			spans = append(spans, log.Span{Start: at, End: end})
		}
		from = end
	}
	return spans
}

// appendURISpans masks each address that begins with the scheme prefix, up to the
// next whitespace or quote; the whole address is the secret, and the scheme is
// matched without regard to case.
func appendURISpans(spans []log.Span, value, prefix string) []log.Span {
	for from := 0; from < len(value); {
		at := indexFold(value, prefix, from)
		if at < 0 {
			break
		}
		end := at + len(prefix)
		for end < len(value) && !isSpaceByte(value[end]) {
			end++
		}
		if end > at+len(prefix) {
			spans = append(spans, log.Span{Start: at, End: end})
		}
		from = end
	}
	return spans
}

func appendPairSpans(spans []log.Span, value string) []log.Span {
	for from := 0; from < len(value); {
		eq := strings.IndexByte(value[from:], '=')
		if eq < 0 {
			break
		}
		eq += from
		from = eq + 1
		start := eq
		for start > 0 && isKeyByte(value[start-1]) {
			start--
		}
		if eq-start == 0 || eq-start > longestPairKey || !isSecretKey(value[start:eq]) {
			continue
		}
		end := from
		for end < len(value) && !isPairEnd(value[end]) {
			end++
		}
		if end > from {
			spans = append(spans, log.Span{Start: from, End: end})
		}
		from = end
	}
	return spans
}

func isSecretKey(key string) bool {
	folded := strings.ToLower(key)
	if _, found := secretPairKeys[folded]; found {
		return true
	}
	_, found := sessionCookieKeys[folded]
	return found
}

func isKeyByte(b byte) bool {
	return isWordByte(b) || b == '_' || b == '-'
}

func isPairEnd(b byte) bool { return b == '&' || b == ';' || isSpaceByte(b) }

// credentialMatcher builds the rule that masks the value following an
// authentication scheme wherever a scheme name in play begins a word and is
// followed by blanks, unless the run after the blanks is a word of schemeProse.
func credentialMatcher(schemes []string) log.Matcher {
	needles := make([]string, 0, len(schemes))
	seen := make(map[string]struct{}, len(schemes))
	for _, scheme := range schemes {
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
		// The spans are handed back as found: pkg/log drops the out-of-range
		// ones, then sorts and merges the rest before applying them.
		return spans
	}
}

// indexFold finds needle in s at or after from, comparing ASCII letters without
// regard to case, and returns an index into s. It scans the original rather than a
// lower-cased copy, whose offsets would not index the original; only ASCII case is folded.
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

func startsWord(s string, at int) bool {
	if at == 0 {
		return true
	}
	return !isWordByte(s[at-1])
}

func isWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return false
}

// schemeProse is the vocabulary a scheme name is followed by when it is written
// about rather than sent: the nouns a sentence about authentication puts after
// "Basic" or "Bearer", and the function words that follow a scheme name.
var schemeProse = map[string]struct{}{
	// What a sentence about a credential names after the scheme.
	"auth": {}, "authentication": {}, "authorization": {},
	"challenge": {}, "challenges": {}, "credential": {}, "credentials": {},
	"header": {}, "headers": {}, "realm": {}, "scheme": {}, "schemes": {},
	"token": {}, "tokens": {},
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"but": {}, "by": {}, "for": {}, "from": {}, "has": {}, "if": {}, "in": {},
	"is": {}, "it": {}, "nor": {}, "not": {}, "of": {}, "on": {}, "or": {},
	"than": {}, "that": {}, "the": {}, "then": {}, "to": {}, "was": {},
	"were": {}, "when": {}, "which": {}, "with": {},
}

// isProse reports whether the run after a scheme name is a word of schemeProse,
// in any case and with the punctuation a sentence puts after a word left off.
func isProse(run string) bool {
	_, listed := schemeProse[strings.ToLower(strings.TrimRight(run, ".,;:!?)="))]
	return listed
}

// valueSpan takes the credential value after a scheme name: from the first byte
// after the blanks separating the two to the next whitespace or quote or the end.
// RFC 9110 separates scheme and credential by one or more blanks.
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

func isSpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '"', '\'':
		return true
	}
	return false
}
