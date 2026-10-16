//go:build integration

package account

import (
	"context"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
)

// A display-name prefix has to read the name's bytes, case-sensitively and
// accent-sensitively, whatever collation the column carries. PostgreSQL orders
// text under the column's collation, and a server built against the GNU C
// library gives a linguistic one by default, which compares case and accents
// together at its first levels; a range left to that collation would read rows
// that are not a byte prefix of the name. This case gives the column such a
// collation — the fixture's server is built against a C library whose own
// collations compare bytes, so an ICU one stands in for that default — and holds
// that the read still matches the prefix's bytes alone.
//
// The case runs only on PostgreSQL: SQLite compares text byte by byte and has no
// collation to give the column that would change the answer.
func TestTheDisplayNamePrefixReadsBytesUnderALocaleCollation(t *testing.T) {
	h := newHarness(t, db.Postgres)
	ctx := context.Background()
	create := func(display, username string) string {
		t.Helper()
		a, err := h.p.Create(ctx, authn.CreateAccount{DisplayName: display, Username: username, Via: "admin"})
		if err != nil {
			t.Fatalf("creating %q: %v", display, err)
		}
		return a.ID.String()
	}
	alice := create("Alice Armstrong", "alice-arm")
	alicia := create("Alicia Brown", "alicia-br")
	lower := create("alice lowercase", "alice-low")
	accent := create("Álice accented", "alice-acc")

	// Set case and accents aside at the collation's first levels, which is what
	// the GNU C library's default does and what makes the byte prefix a different
	// range from the one the statement would read on its own.
	for _, statement := range []string{
		`CREATE COLLATION display_name_locale (provider = icu, locale = 'en-US')`,
		`ALTER TABLE authn_accounts ALTER COLUMN display_name TYPE TEXT COLLATE display_name_locale`,
	} {
		if err := h.handle.Exec(statement).Error; err != nil {
			t.Fatalf("giving display_name a locale collation: %v", err)
		}
	}

	names := func(prefix string) []string {
		t.Helper()
		page, _, err := h.p.List(ctx, authn.AccountQuery{DisplayNamePrefix: prefix})
		if err != nil {
			t.Fatalf("listing display_name_prefix=%q: %v", prefix, err)
		}
		out := make([]string, 0, len(page))
		for _, a := range page {
			out = append(out, a.ID.String())
		}
		slices.Sort(out)
		return out
	}
	sorted := func(ids ...string) []string {
		out := slices.Clone(ids)
		slices.Sort(out)
		return out
	}

	for prefix, want := range map[string][]string{
		// A byte prefix holds the two names spelled with an uppercase A and
		// neither the lowercase nor the accented one.
		"A":     sorted(alice, alicia),
		"Ali":   sorted(alice, alicia),
		"Alice": {alice},
		// Case is significant: only the name spelled with a lowercase a, which
		// the linguistic collation would have read as the same letter.
		"a": {lower},
		// The accented name is read by its own bytes, not folded onto an A.
		"Á": {accent},
		// A digitised prefix reaches only the name stored with those bytes.
		"alice": {lower},
	} {
		if got := names(prefix); !slices.Equal(got, want) {
			t.Errorf("the display-name prefix %q lists %v, want %v", prefix, got, want)
		}
	}
}
