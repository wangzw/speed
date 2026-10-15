package gate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// credentialKeyNames are the attribute names a credential or a one-time secret
// would travel under, as the design lists them. A record written anywhere in the
// process under one of these is masked. The authentication modules that carry
// these values are the ones to register them in their own New; the gateway
// registers the whole set because it is the one module every authentication
// assembly has.
var credentialKeyNames = []string{
	"Authorization", "Cookie", "Set-Cookie", "Credential",
	"password", "new_password", "current_password",
	"code", "otp", "secret", "client_secret",
	"refresh_token", "access_token", "id_token", "token",
	"assertion", "SAMLResponse", "state", "code_verifier", "flow_token",
	"credential",
}

// keySpellings lists every spelling of the names that is registered: the name as
// written, in lower case, and with its first letter capitalised. Key matching is
// an exact lookup — pkg/log stores what it is given and folds nothing — so a name
// registered in one case leaves the others in the clear, and which case a call
// site picked is not something this module can know.
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

// registerNewRules registers what needs no table with the logging capability when
// the assembly carries one: the credential key names and the rule over value
// shapes. It runs in New, before any logger binds an attribute, which is the
// timing a rule needs to govern what a logger bound before it could otherwise
// have kept: a rule only judges what is written after it is registered, and an
// attribute bound with With is judged once, when it is bound.
//
// Absence of logging is legal and registers nothing. Any other failure to take
// the capability up is carried out: reading it as absence would leave the
// credential keys unmasked without a word.
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
// authentication scheme.
//
// The schemes are the ones actually in play: every scheme a backend in this
// assembly takes. Registering a fixed list instead would mask what this build
// does not use and miss what it does. It runs in Init, because the schemes are
// read from the backends' declarations there, and so it is later than a logger's
// first bindings; that is weaker than the timing log offers a module for its own
// bindings, and the weakness is stated with the rule.
func registerValueRule(r log.Redaction, schemes []string) {
	r.AddPattern(ModuleName+".credential", credentialMatcher(schemes))
}

// secretPairKeys are the keys of a key=value pair in a query string whose value is
// a secret, in lower case. A key is compared whole: a longer word that merely
// begins or ends with one, such as secretary or barcode, is not a match.
var secretPairKeys = map[string]struct{}{
	"secret": {}, "code": {}, "access_token": {}, "refresh_token": {},
	"id_token": {}, "client_secret": {}, "samlresponse": {}, "assertion": {},
	"password": {},
}

// sessionCookieKeys are the names the session cookie is written under, in lower
// case: the plain name, and the two prefixed forms the session module issues.
var sessionCookieKeys = map[string]struct{}{
	"sid": {}, "__host-sid": {}, "__secure-sid": {},
}

// longestPairKey bounds the key a pair is looked up under, so that a long run of
// word characters before an '=' is never lower-cased only to be found absent.
const longestPairKey = 16

// tokenPrefixes begin the opaque tokens the authn modules issue, which are
// secrets wherever they are written: the refresh token, the service secret and
// the invitation's acceptance token.
var tokenPrefixes = []string{"spd_rt_", "spd_cs_", "spd_inv_"}

// shapeMatcher masks the values whose shape says they are secrets whatever they
// were logged under: a JWT, a token with one of this capability's prefixes, an
// otpauth:// address, the value of a secret pair in a query string and the value
// of the session cookie's pair.
//
// Like the scheme rule it runs on every resolved string value and every error
// text of every record in the process, so it is a set of byte scans with no
// expression engine behind it, and each scan returns at once for a string that
// lacks its marker. The spans come back in the order found; pkg/log sorts and
// merges them.
func shapeMatcher(value string) []log.Span {
	var spans []log.Span
	spans = appendJWTSpans(spans, value)
	for _, prefix := range tokenPrefixes {
		spans = appendTokenSpans(spans, value, prefix)
	}
	spans = appendURISpans(spans, value, "otpauth://")
	return appendPairSpans(spans, value)
}

// appendJWTSpans masks each run that has the shape of a compact JWT: three
// non-empty base64url segments joined by dots, the first beginning with the
// encoding of '{"'. A run of two segments is not one, and neither is a run of
// four or more, which is some other dotted token and not a JWS.
//
// A full stop that ends a sentence is not part of the token, so one trailing dot
// is left off. A token with an empty signature, which is how an unsecured JWT is
// written, ends in a dot and so reads as two segments: the rule leaves it alone,
// and nothing in this capability issues one.
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

// isJWTByte reports whether b can sit in a compact JWT: the base64url alphabet
// and the dot between segments.
func isJWTByte(b byte) bool { return b == '.' || isKeyByte(b) }

// appendTokenSpans masks each token that begins with prefix, from the prefix to
// the end of the run of URL-safe characters after it. A prefix with nothing after
// it is a word and not a token.
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
// next whitespace or quote. The scheme of a URI is case-insensitive. The whole
// address is the secret: its query carries the seed.
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

// appendPairSpans masks the value of each key=value pair whose key is a secret
// pair key or a session cookie name, from the '=' to the next '&', ';',
// whitespace or quote.
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

// isSecretKey reports whether key, compared without regard to case, is a key
// whose value is a secret.
func isSecretKey(key string) bool {
	folded := strings.ToLower(key)
	if _, found := secretPairKeys[folded]; found {
		return true
	}
	_, found := sessionCookieKeys[folded]
	return found
}

// isKeyByte reports whether b can sit in a key or a URL-safe token: a letter, a
// digit, an underscore or a hyphen.
func isKeyByte(b byte) bool {
	return isWordByte(b) || b == '_' || b == '-'
}

// isPairEnd reports whether b ends the value of a pair.
func isPairEnd(b byte) bool { return b == '&' || b == ';' || isSpaceByte(b) }

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
