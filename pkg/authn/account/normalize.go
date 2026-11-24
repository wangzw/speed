package account

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/slug"
)

const (
	emailMaxBytes  = 254
	prefixMinChars = 3
)

var (
	usernamePrefixShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	phoneShape          = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	phonePrefixShape    = regexp.MustCompile(`^\+[1-9][0-9]{0,14}$`)
)

// errNotWellFormed names the class of identifier and the rule it broke, never
// the value.
func errNotWellFormed(kind authn.IdentifierKind, rule string) error {
	err := fmt.Errorf("%w: a %s %s", authn.ErrInvalidIdentifier, kind, rule)
	if kind == authn.IdentifierUsername {
		return authn.UsernameShape(err)
	}
	return err
}

// NormalizeIdentifier is the form an identifier is stored and looked up in, and
// is applied to both sides. A value outside its kind's form returns an error
// wrapping authn.ErrInvalidIdentifier that names the class and the rule.
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
	folded, ok := slug.Normalize(value)
	switch {
	case !ok:
		return "", errNotWellFormed(authn.IdentifierUsername, "is not 3 to 63 of [a-z0-9-] beginning with a letter or a digit")
	case strings.HasPrefix(folded, authn.StaticPrefix):
		return "", errNotWellFormed(authn.IdentifierUsername, "begins with the prefix of the static credential table")
	case isUUIDShaped(folded):
		return "", errNotWellFormed(authn.IdentifierUsername, "is the shape of an account identifier")
	}
	return folded, nil
}

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

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func normalizePrefix(kind authn.IdentifierKind, prefix string) (string, error) {
	var folded string
	switch kind {
	case authn.IdentifierUsername:
		folded = strings.ToLower(norm.NFKC.String(prefix))
		if !usernamePrefixShape.MatchString(folded) {
			return "", errNotWellFormed(kind, "prefix is not of [a-z0-9-] beginning with a letter or a digit")
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

func prefixBound(prefix string) string {
	last := len(prefix) - 1
	return prefix[:last] + string(prefix[last]+1)
}
