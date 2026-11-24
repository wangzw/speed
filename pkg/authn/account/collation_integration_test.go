//go:build integration

package account

import (
	"context"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
)

// A display-name prefix reads the name's bytes case- and accent-sensitively, whatever collation the column carries.
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
		"A":     sorted(alice, alicia),
		"Ali":   sorted(alice, alicia),
		"Alice": {alice},
		"a":     {lower},

		"Á": {accent},

		"alice": {lower},
	} {
		if got := names(prefix); !slices.Equal(got, want) {
			t.Errorf("the display-name prefix %q lists %v, want %v", prefix, got, want)
		}
	}
}
