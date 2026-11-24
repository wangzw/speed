package i18n

import (
	"context"
	"testing"
	"time"
)

// TestLocaleRoundTripsThroughContext checks that a locale written into a context
// reads back with a found flag.
func TestLocaleRoundTripsThroughContext(t *testing.T) {
	for _, locale := range []Locale{"en-US", "zh-CN", "fr-FR"} {
		ctx := WithLocale(context.Background(), locale)
		got, ok := FromContext(ctx)
		if !ok || got != locale {
			t.Errorf("FromContext(WithLocale(%q)) = %q, %v; want %q, true", locale, got, ok, locale)
		}
	}
}

// TestFromContextReadsAContextWithNoLocaleAsNone checks that a context nobody
// wrote a locale into reports none.
func TestFromContextReadsAContextWithNoLocaleAsNone(t *testing.T) {
	if got, ok := FromContext(context.Background()); ok || got != "" {
		t.Errorf("FromContext(context.Background()) = %q, %v; want \"\", false", got, ok)
	}
}

// TestWithLocaleWritesNothingForAnEmptyLocale checks that an empty locale
// neither introduces a locale nor erases the one beneath it.
func TestWithLocaleWritesNothingForAnEmptyLocale(t *testing.T) {
	parent := WithLocale(context.Background(), "zh-CN")
	got, ok := FromContext(WithLocale(parent, ""))
	if !ok || got != "zh-CN" {
		t.Errorf("FromContext(WithLocale(carrying %q, \"\")) = %q, %v; want %q, true", "zh-CN", got, ok, "zh-CN")
	}
}

// TestTimeZoneRoundTripsThroughContext checks that a time zone written into a
// context reads back with a found flag.
func TestTimeZoneRoundTripsThroughContext(t *testing.T) {
	for _, name := range []string{"Asia/Shanghai", "UTC", "America/New_York"} {
		ctx := WithTimeZone(context.Background(), name)
		got, ok := TimeZoneFromContext(ctx)
		if !ok || got != name {
			t.Errorf("TimeZoneFromContext(WithTimeZone(%q)) = %q, %v; want %q, true", name, got, ok, name)
		}
	}
}

// TestTimeZoneFromContextReadsAContextWithNoneAsNone checks that a context with
// no time zone reports none.
func TestTimeZoneFromContextReadsAContextWithNoneAsNone(t *testing.T) {
	if got, ok := TimeZoneFromContext(context.Background()); ok || got != "" {
		t.Errorf("TimeZoneFromContext(context.Background()) = %q, %v; want \"\", false", got, ok)
	}
}

// TestWithTimeZoneWritesNothingForAnEmptyName checks that an empty name leaves
// a zone already in the layer beneath.
func TestWithTimeZoneWritesNothingForAnEmptyName(t *testing.T) {
	parent := WithTimeZone(context.Background(), "Asia/Shanghai")
	got, ok := TimeZoneFromContext(WithTimeZone(parent, ""))
	if !ok || got != "Asia/Shanghai" {
		t.Errorf("TimeZoneFromContext(WithTimeZone(carrying Asia/Shanghai, \"\")) = %q, %v; want Asia/Shanghai, true", got, ok)
	}
}

// TestValidLocaleTagAcceptsWellShapedTags checks that well-shaped and empty tags
// are accepted.
func TestValidLocaleTagAcceptsWellShapedTags(t *testing.T) {
	for _, tag := range []string{"en-US", "zh-CN", "fr", ""} {
		if !ValidLocaleTag(tag) {
			t.Errorf("ValidLocaleTag(%q) = false; want true", tag)
		}
	}
}

// TestValidLocaleTagRefusesMalformedTags checks that a malformed tag is refused.
func TestValidLocaleTagRefusesMalformedTags(t *testing.T) {
	for _, tag := range []string{"en_US", "1234", "!"} {
		if ValidLocaleTag(tag) {
			t.Errorf("ValidLocaleTag(%q) = true; want false", tag)
		}
	}
}

// TestValidTimeZoneAcceptsLoadableZones checks that a loadable IANA name and the
// empty name are valid.
func TestValidTimeZoneAcceptsLoadableZones(t *testing.T) {
	for _, name := range []string{"Asia/Shanghai", "UTC", ""} {
		if !ValidTimeZone(name) {
			t.Errorf("ValidTimeZone(%q) = false; want true", name)
		}
	}
}

// TestValidTimeZoneRefusesNonIANANames checks that a fixed offset and an
// abbreviation are refused.
func TestValidTimeZoneRefusesNonIANANames(t *testing.T) {
	for _, name := range []string{"+08:00", "CST", "Not/AZone"} {
		if ValidTimeZone(name) {
			t.Errorf("ValidTimeZone(%q) = true; want false", name)
		}
	}
}

// TestValidTimeZoneRefusesTheGoLocalName checks that ValidTimeZone refuses Go's
// "Local" name, which the loader accepts but resolves to the host's zone.
func TestValidTimeZoneRefusesTheGoLocalName(t *testing.T) {
	if ValidTimeZone("Local") {
		t.Error(`ValidTimeZone("Local") = true; want false`)
	}
	if _, err := time.LoadLocation("Local"); err != nil {
		t.Fatalf(`time.LoadLocation("Local") = %v; want the name to load, which is why the refusal is written for it`, err)
	}
}

// TestConfigNamesAreStable checks the two names another module reaches across
// the package boundary.
func TestConfigNamesAreStable(t *testing.T) {
	if ConfigNamespace != "i18n" {
		t.Errorf("ConfigNamespace = %q; want %q", ConfigNamespace, "i18n")
	}
	if DefaultLocaleItem != "default-locale" {
		t.Errorf("DefaultLocaleItem = %q; want %q", DefaultLocaleItem, "default-locale")
	}
}

// TestValueCarriesItsType checks each constructor against its reader and the
// zero of a mismatched kind.
func TestValueCarriesItsType(t *testing.T) {
	s := String("x")
	if KindOf(s) != KindString || StringOf(s) != "x" {
		t.Errorf("String(\"x\") is kind %d, text %q; want kind %d, text %q", KindOf(s), StringOf(s), KindString, "x")
	}
	n := Int(-3)
	if KindOf(n) != KindInt || IntOf(n) != -3 {
		t.Errorf("Int(-3) is kind %d, value %d; want kind %d, value %d", KindOf(n), IntOf(n), KindInt, -3)
	}
	f := Float(1.5)
	if KindOf(f) != KindFloat || FloatOf(f) != 1.5 {
		t.Errorf("Float(1.5) is kind %d, value %v; want kind %d, value %v", KindOf(f), FloatOf(f), KindFloat, 1.5)
	}

	var zero Value
	if KindOf(zero) != KindString || StringOf(zero) != "" {
		t.Errorf("the zero Value is kind %d, text %q; want kind %d, text \"\"", KindOf(zero), StringOf(zero), KindString)
	}

	if IntOf(s) != 0 || FloatOf(s) != 0 || StringOf(n) != "" {
		t.Error("a reader on the wrong kind did not return the zero of that kind")
	}
}

// TestTimeValueCarriesItsInstant checks the time constructor and reader and the
// zero of a mismatched kind.
func TestTimeValueCarriesItsInstant(t *testing.T) {
	want := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	tv := Time(want)
	if KindOf(tv) != KindTime {
		t.Errorf("Time(...) is kind %d; want kind %d", KindOf(tv), KindTime)
	}
	if got := TimeOf(tv); !got.Equal(want) {
		t.Errorf("TimeOf(Time(...)) = %v; want %v", got, want)
	}
	if got := TimeOf(String("x")); !got.IsZero() {
		t.Errorf("TimeOf(a string value) = %v; want the zero time", got)
	}
}
