package i18n

import (
	"regexp"
	"time"
)

// The account attribute keys a user's language and time zone are stored under.
// They are namespaced so a host's own attributes never collide with them, and
// they sit inside the account attribute set the authn capability already
// carries, so a preference reaches every layer through the identity with no
// schema change and no read on the request path.
const (
	// LocaleAttributeKey holds a user's language, as a BCP 47 tag.
	LocaleAttributeKey = "i18n.locale"
	// TimeZoneAttributeKey holds a user's time zone, as an IANA name.
	TimeZoneAttributeKey = "i18n.tz"
)

// localeShape is the grammar a stored locale is held to: a language subtag of
// two or three letters followed by zero or more hyphenated subtags. It is not
// the full BCP 47 grammar, which the engine's matching library parses; it is
// the check a write applies, so that a preference that is plainly not a tag is
// refused rather than stored to fall back from forever.
var localeShape = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// ValidLocaleTag reports whether name is well shaped as a BCP 47 language tag.
// It does not report whether the tag is one this run supports — that is the set
// of locales the catalogs carry, which only the engine knows — so a well shaped
// but unsupported tag is admissible here and falls back at render time.
func ValidLocaleTag(name string) bool {
	return name == "" || localeShape.MatchString(name)
}

// ValidTimeZone reports whether name is a time zone the process can load. The
// empty name is valid and reads as UTC. A fixed offset like "+08:00" and an
// abbreviation like "CST" are not IANA names and are refused: a fixed offset
// cannot follow a region's daylight saving rule, and an abbreviation names
// different zones in different places.
func ValidTimeZone(name string) bool {
	if name == "" {
		return true
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
