package account

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/vislake/speed/pkg/authn"
)

// The bounds an identifier is held to.
const (
	// emailMaxBytes is the longest an email address may be, which is the longest
	// the SMTP path admits.
	emailMaxBytes = 254
	// prefixMinChars is the shortest identifier prefix a listing accepts, so that
	// a listing cannot read the whole table.
	prefixMinChars = 3
)

var (
	// usernameShape is the form a normalized username takes. It holds no @, so a
	// username and an email address are told apart by that character alone.
	usernameShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	// usernamePrefixShape is the form a prefix of a normalized username takes.
	usernamePrefixShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// phoneShape is E.164: a plus sign, a country code that does not begin with
	// zero, and at most fifteen digits in all.
	phoneShape = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	// phonePrefixShape is the form a prefix of an E.164 number takes.
	phonePrefixShape = regexp.MustCompile(`^\+[1-9][0-9]{0,14}$`)
)

// errNotWellFormed is what every normalization failure is. It names the class
// of identifier and the rule it broke, and never the value, which is a person's.
func errNotWellFormed(kind authn.IdentifierKind, rule string) error {
	return fmt.Errorf("%w: a %s %s", authn.ErrInvalidIdentifier, kind, rule)
}

// NormalizeIdentifier is the one form an identifier is stored and looked up in.
// It is applied on both sides, to the value written and to the value asked for,
// because the database compares what it is given and a side that skipped it
// would find nothing and report no error.
//
// A username is put through NFKC and lower-cased, and has to match
// [a-z0-9][a-z0-9._-]{2,63}: it holds no @, does not begin with the static
// credential table's prefix and is not the shape of a UUID, which is what an
// account identifier looks like. An email address is trimmed and lower-cased,
// has to be ASCII, hold exactly one @ with something on each side, and be at
// most 254 bytes; no alias is folded, so a sub-address and a dot stay as written.
// A phone number has to be E.164, a plus sign and 7 to 15 digits, written as it
// is stored: the server does not parse numbers, and a space or a dash is
// refused. Any failure is authn.ErrInvalidIdentifier, and the error never
// carries the value.
func NormalizeIdentifier(kind authn.IdentifierKind, value string) (string, error) {
	switch kind {
	case authn.IdentifierUsername:
		return normalizeUsername(value)
	case authn.IdentifierEmail:
		return normalizeEmail(value)
	case authn.IdentifierPhone:
		if !phoneShape.MatchString(value) {
			return "", errNotWellFormed(kind, "is not an E.164 number")
		}
		return value, nil
	}
	return "", fmt.Errorf("%w: the kind of identifier is not one of the three", authn.ErrInvalidIdentifier)
}

func normalizeUsername(value string) (string, error) {
	folded := strings.ToLower(norm.NFKC.String(value))
	switch {
	case !usernameShape.MatchString(folded):
		return "", errNotWellFormed(authn.IdentifierUsername, "is not 3 to 64 of [a-z0-9._-] beginning with a letter or a digit")
	case strings.HasPrefix(folded, authn.StaticPrefix):
		return "", errNotWellFormed(authn.IdentifierUsername, "begins with the prefix of the static credential table")
	case isUUIDShaped(folded):
		return "", errNotWellFormed(authn.IdentifierUsername, "is the shape of an account identifier")
	}
	return folded, nil
}

// isUUIDShaped reports whether s reads as a UUID in any of the forms the parser
// takes. A username of that shape would be taken for an account identifier by a
// reader that tells the two apart by their look.
func isUUIDShaped(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func normalizeEmail(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > emailMaxBytes {
		return "", errNotWellFormed(authn.IdentifierEmail, "is longer than 254 bytes")
	}
	if !printableASCII(trimmed) {
		return "", errNotWellFormed(authn.IdentifierEmail, "is not printable ASCII")
	}
	local, domain, found := strings.Cut(trimmed, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", errNotWellFormed(authn.IdentifierEmail, "does not hold one @ with a part on each side")
	}
	return strings.ToLower(trimmed), nil
}

// printableASCII reports whether every byte of s is a visible ASCII character,
// the space excluded.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// normalizePrefix is NormalizeIdentifier for the beginning of an identifier: the
// same folding and the same alphabet, none of the whole-value rules. It is what a
// listing by prefix compares with, so the prefix is read the way the stored
// values were written. A prefix shorter than prefixMinChars once folded is
// authn.ErrInvalidArgument.
func normalizePrefix(kind authn.IdentifierKind, prefix string) (string, error) {
	var folded string
	switch kind {
	case authn.IdentifierUsername:
		folded = strings.ToLower(norm.NFKC.String(prefix))
		if !usernamePrefixShape.MatchString(folded) {
			return "", errNotWellFormed(kind, "prefix is not of [a-z0-9._-] beginning with a letter or a digit")
		}
	case authn.IdentifierEmail:
		folded = strings.ToLower(strings.TrimSpace(prefix))
		if len(folded) > emailMaxBytes || !printableASCII(folded) || strings.Count(folded, "@") > 1 {
			return "", errNotWellFormed(kind, "prefix is not printable ASCII with at most one @")
		}
	case authn.IdentifierPhone:
		folded = prefix
		if !phonePrefixShape.MatchString(folded) {
			return "", errNotWellFormed(kind, "prefix is not the beginning of an E.164 number")
		}
	default:
		return "", fmt.Errorf("%w: the kind of identifier is not one of the three", authn.ErrInvalidIdentifier)
	}
	if len(folded) < prefixMinChars {
		return "", fmt.Errorf("%w: an identifier prefix is at least %d characters", authn.ErrInvalidArgument, prefixMinChars)
	}
	return folded, nil
}

// prefixBound is the least string above every string that begins with prefix,
// which is the prefix with its last byte raised by one. A prefix is ASCII once
// normalized, so the last byte is below 0x7f and the raise cannot overflow.
func prefixBound(prefix string) string {
	last := len(prefix) - 1
	return prefix[:last] + string(prefix[last]+1)
}
