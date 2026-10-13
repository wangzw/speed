package i18n

import (
	"context"
	"testing"
)

// TestLocaleRoundTripsThroughContext holds the write-then-read pair: the value
// written is the value read back, and the read reports that it found one.
func TestLocaleRoundTripsThroughContext(t *testing.T) {
	for _, locale := range []Locale{"en-US", "zh-CN", "fr-FR"} {
		ctx := WithLocale(context.Background(), locale)
		got, ok := FromContext(ctx)
		if !ok || got != locale {
			t.Errorf("FromContext(WithLocale(%q)) = %q, %v; want %q, true", locale, got, ok, locale)
		}
	}
}

// TestFromContextReadsAContextWithNoLocaleAsNone holds that the read applies no
// fallback: a context nobody wrote a locale into reports none, not the default.
func TestFromContextReadsAContextWithNoLocaleAsNone(t *testing.T) {
	if got, ok := FromContext(context.Background()); ok || got != "" {
		t.Errorf("FromContext(context.Background()) = %q, %v; want \"\", false", got, ok)
	}
}

// TestWithLocaleWritesNothingForAnEmptyLocale holds that an empty locale is not
// written: the layer already carrying one is what a read still sees, so an empty
// write neither introduces a locale nor erases the one beneath it.
func TestWithLocaleWritesNothingForAnEmptyLocale(t *testing.T) {
	parent := WithLocale(context.Background(), "zh-CN")
	got, ok := FromContext(WithLocale(parent, ""))
	if !ok || got != "zh-CN" {
		t.Errorf("FromContext(WithLocale(carrying %q, \"\")) = %q, %v; want %q, true", "zh-CN", got, ok, "zh-CN")
	}
}

// TestTimeZoneRoundTripsThroughContext holds the time zone write-then-read pair.
func TestTimeZoneRoundTripsThroughContext(t *testing.T) {
	for _, name := range []string{"Asia/Shanghai", "UTC", "America/New_York"} {
		ctx := WithTimeZone(context.Background(), name)
		got, ok := TimeZoneFromContext(ctx)
		if !ok || got != name {
			t.Errorf("TimeZoneFromContext(WithTimeZone(%q)) = %q, %v; want %q, true", name, got, ok, name)
		}
	}
}

// TestTimeZoneFromContextReadsAContextWithNoneAsNone holds that a context nobody
// wrote a time zone into reports none, leaving the UTC fallback to the resolver.
func TestTimeZoneFromContextReadsAContextWithNoneAsNone(t *testing.T) {
	if got, ok := TimeZoneFromContext(context.Background()); ok || got != "" {
		t.Errorf("TimeZoneFromContext(context.Background()) = %q, %v; want \"\", false", got, ok)
	}
}

// TestWithTimeZoneWritesNothingForAnEmptyName holds that an empty name is not
// written: a zone already in the layer beneath survives the empty write.
func TestWithTimeZoneWritesNothingForAnEmptyName(t *testing.T) {
	parent := WithTimeZone(context.Background(), "Asia/Shanghai")
	got, ok := TimeZoneFromContext(WithTimeZone(parent, ""))
	if !ok || got != "Asia/Shanghai" {
		t.Errorf("TimeZoneFromContext(WithTimeZone(carrying Asia/Shanghai, \"\")) = %q, %v; want Asia/Shanghai, true", got, ok)
	}
}

// TestValidLocaleTagAcceptsWellShapedTags holds the grammar a write applies: a
// two-or-three-letter language with optional hyphenated subtags, and the empty
// tag, are well shaped.
func TestValidLocaleTagAcceptsWellShapedTags(t *testing.T) {
	for _, tag := range []string{"en-US", "zh-CN", "fr", ""} {
		if !ValidLocaleTag(tag) {
			t.Errorf("ValidLocaleTag(%q) = false; want true", tag)
		}
	}
}

// TestValidLocaleTagRefusesMalformedTags holds that a tag plainly not a BCP 47
// tag is refused rather than stored to fall back from forever.
func TestValidLocaleTagRefusesMalformedTags(t *testing.T) {
	for _, tag := range []string{"en_US", "1234", "!"} {
		if ValidLocaleTag(tag) {
			t.Errorf("ValidLocaleTag(%q) = true; want false", tag)
		}
	}
}

// TestValidTimeZoneAcceptsLoadableZones holds that an IANA name the process can
// load, and the empty name that reads as UTC, are valid.
func TestValidTimeZoneAcceptsLoadableZones(t *testing.T) {
	for _, name := range []string{"Asia/Shanghai", "UTC", ""} {
		if !ValidTimeZone(name) {
			t.Errorf("ValidTimeZone(%q) = false; want true", name)
		}
	}
}

// TestValidTimeZoneRefusesNonIANANames holds that a fixed offset and an
// abbreviation, neither of which follows a region's rule, are refused.
func TestValidTimeZoneRefusesNonIANANames(t *testing.T) {
	for _, name := range []string{"+08:00", "CST", "Not/AZone"} {
		if ValidTimeZone(name) {
			t.Errorf("ValidTimeZone(%q) = true; want false", name)
		}
	}
}

// TestConfigNamesAreStable pins the two names another module reaches for across
// the package boundary: the SMS module reads the same default-locale item under
// the same namespace, so a change here silently unfreezes the shared default.
func TestConfigNamesAreStable(t *testing.T) {
	if ConfigNamespace != "i18n" {
		t.Errorf("ConfigNamespace = %q; want %q", ConfigNamespace, "i18n")
	}
	if DefaultLocaleItem != "default-locale" {
		t.Errorf("DefaultLocaleItem = %q; want %q", DefaultLocaleItem, "default-locale")
	}
}
