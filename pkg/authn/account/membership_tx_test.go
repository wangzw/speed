package account

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

func membershipOf(t *testing.T, h *harness, id authn.AccountID, tenant string) (authn.Membership, bool) {
	t.Helper()
	m, found, err := h.p.Membership(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("reading the membership: %v", err)
	}
	return m, found
}

// PutMembershipTx writes the same row Put writes and hands back an after-commit publication.
func TestPutMembershipTxMatchesPut(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		plain := h.user("plain", "")
		seamed := h.user("seamed", "")
		want := authn.Membership{Account: plain, Tenant: "acme", Active: true, Source: "initial"}
		if err := h.p.Put(t.Context(), want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got := want
		got.Account = seamed
		var after func(context.Context) error
		if err := h.handle.Transaction(func(tx *gorm.DB) error {
			var err error
			after, err = h.p.PutMembershipTx(t.Context(), tx, got)
			return err
		}); err != nil {
			t.Fatalf("PutMembershipTx: %v", err)
		}
		if after == nil {
			t.Fatal("PutMembershipTx wrote a row and returned no after closure")
		}
		if err := after(t.Context()); err != nil {
			t.Fatalf("running the after closure: %v", err)
		}

		a, foundA := membershipOf(t, h, plain, "acme")
		b, foundB := membershipOf(t, h, seamed, "acme")
		if !foundA || !foundB {
			t.Fatalf("the two writes read back as (%+v, %v) and (%+v, %v), want both rows", a, foundA, b, foundB)
		}
		a.Account, b.Account = "", ""
		a.CreatedAt, b.CreatedAt = a.CreatedAt.UTC(), b.CreatedAt.UTC()
		if a != b {
			t.Errorf("Put wrote %+v and PutMembershipTx wrote %+v, want the same row", a, b)
		}
		if n := len(h.bus.of(authn.EventMembershipChanged)); n != 2 {
			t.Errorf("%d membership events were published, want one per write", n)
		}
	})
}

// PutMembershipTx writes in the caller's transaction: a rollback leaves no row and publishes nothing.
func TestPutMembershipTxRollsBackWithItsCaller(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		id := h.user("alice", "")
		rollback := errors.New("caller rolled back")
		var after func(context.Context) error
		err := h.handle.Transaction(func(tx *gorm.DB) error {
			var err error
			after, err = h.p.PutMembershipTx(t.Context(), tx,
				authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "initial"})
			if err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("the caller's transaction returned %v, want the rollback", err)
		}
		if after == nil {
			t.Fatal("PutMembershipTx wrote a row inside the transaction and returned no after closure")
		}
		if n := h.count(tableMemberships, "account_id = ?", id.String()); n != 0 {
			t.Errorf("the rolled-back membership is %d row(s), want none", n)
		}
		if n := len(h.bus.of(authn.EventMembershipChanged)); n != 0 {
			t.Errorf("%d membership events were published for a rolled-back write, want none", n)
		}
	})
}

// RemoveMembershipTx removes the row in the caller's transaction and returns the after-commit publication.
func TestRemoveMembershipTxMatchesTheDeletionPath(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		id := h.user("alice", "")
		if err := h.p.Put(t.Context(), authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "initial"}); err != nil {
			t.Fatalf("writing the membership: %v", err)
		}
		var after func(context.Context) error
		if err := h.handle.Transaction(func(tx *gorm.DB) error {
			var err error
			after, err = h.p.RemoveMembershipTx(t.Context(), tx, id, "acme")
			return err
		}); err != nil {
			t.Fatalf("RemoveMembershipTx: %v", err)
		}
		if after == nil {
			t.Fatal("RemoveMembershipTx removed a row and returned no after closure")
		}
		if _, found := membershipOf(t, h, id, "acme"); found {
			t.Error("the membership is still there after the committing transaction")
		}
		if err := after(t.Context()); err != nil {
			t.Fatalf("running the after closure: %v", err)
		}
		removed := false
		for _, evt := range h.bus.of(authn.EventMembershipChanged) {
			if payload, ok := evt.Payload.(authn.MembershipChanged); ok && payload.Change == "removed" {
				removed = true
			}
		}
		if !removed {
			t.Errorf("no membership-removed event was published: %+v", h.bus.of(authn.EventMembershipChanged))
		}
	})
}

// A caller that rolls its transaction back keeps the membership RemoveMembershipTx removed.
func TestRemoveMembershipTxRollsBackWithItsCaller(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		id := h.user("alice", "")
		if err := h.p.Put(t.Context(), authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "initial"}); err != nil {
			t.Fatalf("writing the membership: %v", err)
		}
		rollback := errors.New("caller rolled back")
		err := h.handle.Transaction(func(tx *gorm.DB) error {
			if _, err := h.p.RemoveMembershipTx(t.Context(), tx, id, "acme"); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("the caller's transaction returned %v, want the rollback", err)
		}
		if _, found := membershipOf(t, h, id, "acme"); !found {
			t.Error("the rolled-back removal left no membership, want the row kept")
		}
	})
}
