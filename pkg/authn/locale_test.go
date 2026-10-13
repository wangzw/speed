package authn

import "testing"

// TestLocaleOfReadsTheEmptyValueAsTheDefault holds the mapping's one job: an
// empty value names no language and reads as LocaleEN, and a language the caller
// named is returned as it is.
func TestLocaleOfReadsTheEmptyValueAsTheDefault(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", LocaleEN},
		{LocaleEN, LocaleEN},
		{LocaleZH, LocaleZH},
	} {
		if got := LocaleOf(c.in); got != c.want {
			t.Errorf("LocaleOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLocaleOfDoesNotReclassifyAValueOutsideTheClosedSet holds the pass-through
// branch: a value the caller named that the closed set does not hold is returned
// unchanged, not replaced by the default. No caller exercises this branch — the
// callers in the SMS module pass a value the boundary has already accepted or
// refused against the raw input — so an implementation that read every non-empty
// value as LocaleZH would pass every other test in this package. This asserts the
// value stays the caller's own, for the caller to refuse.
func TestLocaleOfDoesNotReclassifyAValueOutsideTheClosedSet(t *testing.T) {
	const outside = "fr-FR"
	if got := LocaleOf(outside); got != outside {
		t.Errorf("LocaleOf(%q) = %q, want the value unchanged", outside, got)
	}
}
