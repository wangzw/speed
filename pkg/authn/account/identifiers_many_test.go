package account

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestIdentifiersManyAnswersEachAccountAsItsSingleReadDoes holds the batch read
// to the single read's answer: the same identifiers in the same order, carrying
// the same verification way, which is what the management view derives its
// proven flag from. The two rows of the first account are written in an order
// that agrees with neither the created_at, id order the single read returns nor
// the identifier order, so a batch that returned either of those would fail
// here, and so would one that dropped the verification way.
func TestIdentifiersManyAnswersEachAccountAsItsSingleReadDoes(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		ids := seedAccounts(t, h, 2, authn.KindUser)
		a, b := authn.AccountID(ids[0]), authn.AccountID(ids[1])
		base := h.clock.read()
		rows := []identifierRow{
			{
				ID: "id-a", AccountID: a.String(), Kind: string(authn.IdentifierUsername), Value: "alpha",
				VerifiedAt: base, VerifiedVia: authn.VerifiedViaAdmin, CreatedAt: base.Add(time.Second),
			},
			{
				ID: "id-z", AccountID: a.String(), Kind: string(authn.IdentifierEmail), Value: "z@example.com",
				VerifiedAt: base, VerifiedVia: authn.VerifiedViaEmailCode, CreatedAt: base,
			},
			{
				ID: "id-m", AccountID: b.String(), Kind: string(authn.IdentifierPhone), Value: "+15550000003",
				VerifiedAt: base, VerifiedVia: authn.VerifiedViaSMSCode, CreatedAt: base,
			},
		}
		if err := h.handle.Create(&rows).Error; err != nil {
			t.Fatalf("writing identifiers: %v", err)
		}

		got, err := h.p.IdentifiersMany(ctx, []authn.AccountID{a, b})
		if err != nil {
			t.Fatalf("reading the identifiers of two accounts: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("the batch answers for %d accounts, want 2: %v", len(got), got)
		}
		for _, id := range []authn.AccountID{a, b} {
			want, err := h.p.Identifiers(ctx, id)
			if err != nil {
				t.Fatalf("reading %q on its own: %v", id, err)
			}
			held, ok := got[id]
			if !ok {
				t.Fatalf("the batch has no entry for %q", id)
			}
			if !reflect.DeepEqual(held, want) {
				t.Errorf("IdentifiersMany(%q) = %+v, want the single read's %+v", id, held, want)
			}
		}
	})
}

// TestIdentifiersManyOfNoAccountsIsAnEmptyMap holds the empty input's answer: an
// empty map and no error, so a page with no account to read costs no statement.
func TestIdentifiersManyOfNoAccountsIsAnEmptyMap(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		for _, ids := range [][]authn.AccountID{nil, {}} {
			got, err := h.p.IdentifiersMany(context.Background(), ids)
			if err != nil {
				t.Errorf("IdentifiersMany(%v) = %v, want no error", ids, err)
			}
			if got == nil {
				t.Errorf("IdentifiersMany(%v) = nil, want an empty map", ids)
			}
			if len(got) != 0 {
				t.Errorf("IdentifiersMany(%v) = %v, want an empty map", ids, got)
			}
		}
	})
}

// TestIdentifiersManyLeavesOutWhatItsSingleReadReportsNotFound holds the batch's
// answer for an unknown account and a tombstone: no entry, so a page that holds
// one keeps rendering and the row shows no identifiers, while the single read
// still reports ErrAccountNotFound for either. A stale identifier left behind a
// deletion is not the tombstone's answer.
func TestIdentifiersManyLeavesOutWhatItsSingleReadReportsNotFound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		live := authn.AccountID(seedAccounts(t, h, 1, authn.KindUser)[0])
		gone := h.user("gone", "")
		if err := h.p.Delete(ctx, gone, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		now := h.clock.read()
		if err := h.handle.Create(&identifierRow{
			ID: "id-stale", AccountID: gone.String(), Kind: string(authn.IdentifierEmail),
			Value: "gone@example.com", VerifiedAt: now, VerifiedVia: authn.VerifiedViaEmailCode, CreatedAt: now,
		}).Error; err != nil {
			t.Fatalf("writing a stale identifier: %v", err)
		}
		const unknown authn.AccountID = "never-was"

		got, err := h.p.IdentifiersMany(ctx, []authn.AccountID{live, gone, unknown})
		if err != nil {
			t.Fatalf("reading a page with a tombstone and an unknown account: %v", err)
		}
		if _, ok := got[live]; !ok {
			t.Errorf("the batch has no entry for the live account %q", live)
		}
		for _, id := range []authn.AccountID{gone, unknown} {
			if held, ok := got[id]; ok {
				t.Errorf("the batch carries an entry for %q, want none: %+v", id, held)
			}
			if _, err := h.p.Identifiers(ctx, id); !errors.Is(err, authn.ErrAccountNotFound) {
				t.Errorf("the single read of %q = %v, want ErrAccountNotFound", id, err)
			}
		}
	})
}
