package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidArgument reports a value a write refused because it is outside what
// the call admits: an attribute set beyond its bounds, a reason that is not one
// of the closed classes, a verification that is none of the forms, a display
// name that is too long. A failure wraps it with a fixed word for the class and
// never with the value, which may be a host's or a person's.
var ErrInvalidArgument = errors.New("authn: an argument is outside what the call admits")

// The reserved attribute-key prefixes. A key under either belongs to this
// capability and not to a host: idp. carries the attributes an enterprise IdP's
// mapping produced for one login, authn. is kept for what the capability itself
// may write later. Whoever writes an attribute keeps the reservation; the
// gateway cannot tell this capability's own backends from a host's and does not
// look.
const (
	ReservedPrefixIDP   = "idp."
	ReservedPrefixAuthn = "authn."
)

// The bounds of the account attribute set, the host-writable part of
// Identity.Attributes. They are the only declaration of these numbers: the
// account module that stores the set, the session module that reads it and the
// login engine that merges it with the session's idp.* attributes all refer to
// them, and none writes a number of its own.
const (
	// AccountAttributesMax is the most entries an account may carry.
	AccountAttributesMax = 24
	// AccountAttributeBytesMax is the most the keys and values of an account's
	// attributes may come to together, in bytes.
	AccountAttributeBytesMax = 2 << 10
	// AttributeKeyMax is the longest an attribute key may be, in bytes.
	AttributeKeyMax = 64
	// AccountAttributeValueMax is the longest an account attribute value may
	// be, in bytes.
	AccountAttributeValueMax = 256
)

// The bounds of the idp.* attributes a session payload carries, which are
// written by the IdP attribute mapping of one login and live and die with that
// session. The key bound counts the prefix.
const (
	// SessionIDPAttributesMax is the most idp.* entries a session payload may
	// carry.
	SessionIDPAttributesMax = 8
	// SessionIDPKeyMax is the longest an idp.* key may be, prefix included, in
	// bytes. A mapped attribute name is at most this less len(ReservedPrefixIDP).
	SessionIDPKeyMax = 32
	// SessionIDPValueMax is the longest an idp.* value may be, in bytes; a longer
	// value is cut at a character boundary with TruncateUTF8.
	SessionIDPValueMax = 128
)

// IdentityAttributesMax and IdentityAttributeBytesMax are the totals the
// gateway holds a stamped identity's attributes to. They are the gateway's
// numbers, repeated here only so that the sub-bounds above can be shown to add
// up to less than them; gate/attribute_bounds_test.go holds the three against
// the gateway's own constants.
const (
	IdentityAttributesMax     = 32
	IdentityAttributeBytesMax = 4 << 10
)

var attributeKeyShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var (
	errAttributeCount    = fmt.Errorf("%w: too many attributes", ErrInvalidArgument)
	errAttributeKey      = fmt.Errorf("%w: an attribute key is not well formed", ErrInvalidArgument)
	errAttributeReserved = fmt.Errorf("%w: an attribute key is under a reserved prefix", ErrInvalidArgument)
	errAttributeValue    = fmt.Errorf("%w: an attribute value is not well formed", ErrInvalidArgument)
	errAttributeSize     = fmt.Errorf("%w: the attributes are too large", ErrInvalidArgument)
)

// CheckAccountAttributes reports whether m is an admissible account attribute
// set: at most AccountAttributesMax entries; keys matching
// [a-z0-9][a-z0-9._-]{0,63} and under neither reserved prefix; values of valid
// UTF-8 with no control character and at most AccountAttributeValueMax bytes;
// keys and values together at most AccountAttributeBytesMax bytes. The nil and
// empty sets are admissible. The error names the class of the failure and never
// echoes a key or a value, and wraps ErrInvalidArgument.
func CheckAccountAttributes(m map[string]string) error {
	if len(m) > AccountAttributesMax {
		return errAttributeCount
	}
	size := 0
	for k, v := range m {
		if !attributeKeyShape.MatchString(k) {
			return errAttributeKey
		}
		if strings.HasPrefix(k, ReservedPrefixIDP) || strings.HasPrefix(k, ReservedPrefixAuthn) {
			return errAttributeReserved
		}
		if !plainText(v, AccountAttributeValueMax) {
			return errAttributeValue
		}
		size += len(k) + len(v)
	}
	if size > AccountAttributeBytesMax {
		return errAttributeSize
	}
	return nil
}

// CheckIDPAttributes reports whether m is an admissible set of the idp.*
// attributes of a session payload: at most SessionIDPAttributesMax entries,
// every key under ReservedPrefixIDP with something after it, shaped as an
// account attribute key and at most SessionIDPKeyMax bytes long, every value of
// valid UTF-8 with no control character and at most SessionIDPValueMax bytes.
// A value the IdP mapping produced is cut with TruncateUTF8 first; this check
// refuses what is still too long. The error never echoes a key or a value, and
// wraps ErrInvalidArgument.
func CheckIDPAttributes(m map[string]string) error {
	if len(m) > SessionIDPAttributesMax {
		return errAttributeCount
	}
	for k, v := range m {
		if !strings.HasPrefix(k, ReservedPrefixIDP) || len(k) == len(ReservedPrefixIDP) ||
			!attributeKeyShape.MatchString(k) || len(k) > SessionIDPKeyMax {
			return errAttributeKey
		}
		if !plainText(v, SessionIDPValueMax) {
			return errAttributeValue
		}
	}
	return nil
}

func plainText(v string, limit int) bool {
	return len(v) <= limit && utf8.ValidString(v) && strings.IndexFunc(v, unicode.IsControl) < 0
}

// TruncateUTF8 returns s cut to at most limit bytes without splitting a
// character: the longest prefix of s that is at most limit bytes long and ends
// on a character boundary. A limit below zero reads as zero. A string that is
// not valid UTF-8 is cut at the same bound and the cut still never lands inside
// a well-formed character.
func TruncateUTF8(s string, limit int) string {
	if limit < 0 {
		limit = 0
	}
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
