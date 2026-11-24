package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidArgument reports a value a write refused as outside what the call
// admits. A failure wraps it with a fixed word for the class and never with the
// value.
var ErrInvalidArgument = errors.New("authn: an argument is outside what the call admits")

// Reserved attribute-key prefixes: a key under either belongs to this
// capability, not to a host. idp. carries the IdP mapping's attributes for one
// login; authn. is kept for what the capability itself may write.
const (
	ReservedPrefixIDP   = "idp."
	ReservedPrefixAuthn = "authn."
)

// The bounds of the account attribute set, the host-writable part of
// Identity.Attributes.
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

// IdentityAttributesMax and IdentityAttributeBytesMax are the totals the gateway
// holds a stamped identity's attributes to.
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
// set: the count, total size, key shape and value limits declared above, with no
// key under a reserved prefix. The error never echoes a key or a value.
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

// CheckIDPAttributes reports whether m is an admissible set of a session
// payload's idp.* attributes: the bounds above, every key under
// ReservedPrefixIDP. A producer truncates a value first; this refuses the rest.
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

// TruncateUTF8 returns the longest prefix of s that is at most limit bytes and
// ends on a character boundary; a limit below zero reads as zero and a
// non-UTF-8 s is cut without landing inside a well-formed character.
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
