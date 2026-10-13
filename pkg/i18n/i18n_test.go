package i18n

import (
	"context"
	"testing"
	"time"
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

// TestValidTimeZoneRefusesTheGoLocalName holds that Go's special location name
// "Local" is refused. It is not an IANA name: time.LoadLocation resolves it to
// whatever zone the host happens to run in, not to a zone the name fixes, so a
// stored "Local" would render user-visible text in the server's own zone rather
// than reading as UTC the way an unloadable name does. The test pins the reader
// behaviour that motivates the refusal — the loader accepts the name and returns
// a location for it — so the refusal is not merely against the literal string.
// "UTC", by contrast, is an IANA zone identifier that resolves to UTC, and the
// sibling accept case keeps it valid.
func TestValidTimeZoneRefusesTheGoLocalName(t *testing.T) {
	if ValidTimeZone("Local") {
		t.Error(`ValidTimeZone("Local") = true; want false`)
	}
	if _, err := time.LoadLocation("Local"); err != nil {
		t.Fatalf(`time.LoadLocation("Local") = %v; want the name to load, which is why the refusal is written for it`, err)
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

// TestValueCarriesItsType holds the typed-argument value type: each constructor
// builds a value whose kind and payload read back, the zero Value reads as an
// empty string, and a reader called on the wrong kind finds the zero of that
// kind.
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

	// Each reader returns the zero of its own kind for a value of another kind.
	if IntOf(s) != 0 || FloatOf(s) != 0 || StringOf(n) != "" {
		t.Error("a reader on the wrong kind did not return the zero of that kind")
	}
}

// TestTimeValueCarriesItsInstant holds the fourth value kind: the time
// constructor and reader are the pair, the kind is KindTime, and the reader
// called on another kind finds the zero time.
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
