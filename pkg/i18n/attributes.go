package i18n

import (
	"regexp"
	"time"
)

const (
	// LocaleAttributeKey holds a user's language, as a BCP 47 tag.
	LocaleAttributeKey = "i18n.locale"
	// TimeZoneAttributeKey holds a user's time zone, as an IANA name.
	TimeZoneAttributeKey = "i18n.tz"
)

var localeShape = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// ValidLocaleTag reports whether name is well shaped as a BCP 47 language tag.
// It does not report whether this run supports the tag.
func ValidLocaleTag(name string) bool {
	return name == "" || localeShape.MatchString(name)
}

// ValidTimeZone reports whether name is a time zone the process can load. The
// empty name is valid and reads as UTC. A fixed offset, an abbreviation and Go's
// special name "Local" are refused; "UTC" is accepted.
func ValidTimeZone(name string) bool {
	if name == "" {
		return true
	}
	if name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
