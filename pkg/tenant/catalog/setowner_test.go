package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// TestSetOwnerWritesOnlyTheOwnerColumn checks that a transfer writes the owner
// column and nothing else.
func TestSetOwnerWritesOnlyTheOwnerColumn(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants "+
		"(id, name, status, disabled_by, owner_account, display_name, description) VALUES "+
		"('t_acme', 'acme', 0, 1, 'acct-1', 'Acme', 'A description.')")
	ctx := context.Background()
	before, ok := rowOf(t, r, "t_acme")
	if !ok {
		t.Fatal("the tenant is not in the table")
	}

	if err := r.SetOwner(ctx, "t_acme", "acct-2"); err != nil {
		t.Fatalf("transferring the owner: %v", err)
	}

	after, ok := rowOf(t, r, "t_acme")
	if !ok {
		t.Fatal("the tenant left the table")
	}
	if after.OwnerAccount != "acct-2" {
		t.Errorf("the owner reads %q, want acct-2", after.OwnerAccount)
	}
	if after.ID != before.ID || after.Name != before.Name || after.Status != before.Status ||
		after.DisabledBy != before.DisabledBy || after.DisplayName != before.DisplayName ||
		after.Description != before.Description || !after.CreatedAt.Equal(before.CreatedAt) ||
		!sameTime(after.DeletedAt, before.DeletedAt) {
		t.Errorf("SetOwner changed more than the owner: before %+v, after %+v", before, after)
	}

	got, found, err := r.Get(ctx, "t_acme")
	if err != nil || !found {
		t.Fatalf("reading the tenant back: found=%v err=%v", found, err)
	}
	if got.OwnerAccount != "acct-2" {
		t.Errorf("the snapshot carries the owner as %q, want acct-2", got.OwnerAccount)
	}
}

// TestSetOwnerRefusesATombstoneAndAnUnknownTenant checks that SetOwner refuses
// a deleted row and an unknown identifier and writes nothing for either.
func TestSetOwnerRefusesATombstoneAndAnUnknownTenant(t *testing.T) {
	ctx := context.Background()
	t.Run("a tombstone", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
		if err := r.SetOwner(ctx, "t_acme", "acct-2"); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Fatalf("transferring a tombstone gave %v, want ErrTenantDeleted", err)
		}
		held, _ := rowOf(t, r, "t_acme")
		if held.OwnerAccount != "" || held.Status != int(tenant.StatusDeleted) {
			t.Errorf("the refused transfer left %+v, want the tombstone as it was", held)
		}
	})
	t.Run("an unknown tenant", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetOwner(ctx, "t_ghost", "acct-2"); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("transferring an identifier no row carries gave %v, want ErrInvalidTenant", err)
		}
		if countRows(t, r) != 1 {
			t.Fatal("the refused transfer wrote a row")
		}
	})
}

// TestSetOwnerRefusesAnOwnerOutsideTheDomain checks that an empty, blank or
// control-character owner is refused before the table is touched.
func TestSetOwnerRefusesAnOwnerOutsideTheDomain(t *testing.T) {
	for name, owner := range map[string]string{
		"an empty owner":       "",
		"a blank in the owner": "acct 1",
		"a control character":  "acct\t1",
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, insert("t_acme", "acme"))
			if err := r.SetOwner(context.Background(), "t_acme", owner); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("setting the owner to %q gave %v, want ErrInvalidTenant", owner, err)
			}
			held, _ := rowOf(t, r, "t_acme")
			if held.OwnerAccount != "" {
				t.Fatalf("the refused write left the owner as %q, want none", held.OwnerAccount)
			}
		})
	}
}

// TestSetOwnerIsIdempotent checks that writing the owner a row already carries
// returns nil and triggers no reread.
func TestSetOwnerIsIdempotent(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants (id, name, status, owner_account) VALUES "+
		"('t_acme', 'acme', 1, 'acct-1')")
	reads := countFullReads(t, r)

	if err := r.SetOwner(context.Background(), "t_acme", "acct-1"); err != nil {
		t.Fatalf("repeating a transfer gave %v, want nil", err)
	}
	if got := reads.Load(); got != 0 {
		t.Errorf("a repeated transfer reread the whole roster %d times, want none", got)
	}
}

// TestSetOwnerIfEmptyRepairsOnlyAnOwnerlessRow checks that SetOwnerIfEmpty
// fills an ownerless row and reports the write, leaves an owner that arrived
// since the read, and refuses identifiers and tombstones as SetOwner does.
func TestSetOwnerIfEmptyRepairsOnlyAnOwnerlessRow(t *testing.T) {
	ctx := context.Background()

	t.Run("an ownerless row is filled", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		wrote, err := r.SetOwnerIfEmpty(ctx, "t_acme", "acct-repair")
		if err != nil {
			t.Fatalf("repairing the owner: %v", err)
		}
		if !wrote {
			t.Error("the repair of an ownerless row reported no write")
		}
		held, _ := rowOf(t, r, "t_acme")
		if held.OwnerAccount != "acct-repair" {
			t.Errorf("the owner reads %q, want acct-repair", held.OwnerAccount)
		}
	})

	t.Run("a standing owner survives", func(t *testing.T) {
		r := rosterOver(t, "INSERT INTO tenants (id, name, status, owner_account) VALUES "+
			"('t_acme', 'acme', 1, 'acct-standing')")
		wrote, err := r.SetOwnerIfEmpty(ctx, "t_acme", "acct-repair")
		if err != nil {
			t.Fatalf("repairing over a standing owner: %v", err)
		}
		if wrote {
			t.Error("the repair wrote over a standing owner")
		}
		held, _ := rowOf(t, r, "t_acme")
		if held.OwnerAccount != "acct-standing" {
			t.Errorf("the standing owner became %q, want it left alone", held.OwnerAccount)
		}
	})

	t.Run("an unknown tenant", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if _, err := r.SetOwnerIfEmpty(ctx, "t_ghost", "acct-repair"); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("repairing an identifier no row carries gave %v, want ErrInvalidTenant", err)
		}
	})

	t.Run("a tombstone", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
		if _, err := r.SetOwnerIfEmpty(ctx, "t_acme", "acct-repair"); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Fatalf("repairing a tombstone gave %v, want ErrTenantDeleted", err)
		}
		held, _ := rowOf(t, r, "t_acme")
		if held.OwnerAccount != "" {
			t.Errorf("the refused repair left the owner as %q, want none", held.OwnerAccount)
		}
	})
}

// TestCreateWritesTheOwnerAndPutLeavesItAlone checks that Create writes the
// value's owner while Put changes state and disable source without transferring
// the owner or changing the name.
func TestCreateWritesTheOwnerAndPutLeavesItAlone(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()

	if err := r.Create(ctx, tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive, OwnerAccount: "acct-1",
	}); err != nil {
		t.Fatalf("creating a tenant with an owner: %v", err)
	}
	created, ok, err := r.Get(ctx, "t_acme")
	if err != nil || !ok {
		t.Fatalf("reading the created tenant: found=%v err=%v", ok, err)
	}
	if created.OwnerAccount != "acct-1" {
		t.Fatalf("the created tenant's owner is %q, want acct-1", created.OwnerAccount)
	}

	if err = r.Put(ctx, tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusDisabled, OwnerAccount: "acct-2",
	}); err != nil {
		t.Fatalf("rewriting the tenant: %v", err)
	}
	rewritten, _, err := r.Get(ctx, "t_acme")
	if err != nil {
		t.Fatalf("reading the rewritten tenant: %v", err)
	}
	if rewritten.OwnerAccount != "acct-1" {
		t.Errorf("Put transferred the owner to %q, want acct-1 left as Create wrote it", rewritten.OwnerAccount)
	}
	if rewritten.Name != "acme" || rewritten.Status != tenant.StatusDisabled {
		t.Errorf("the rewrite left %+v, want the name it was created with and the new state", rewritten)
	}
}

// TestReadRefusesAStoredOwnerOutsideTheDomain checks that a stored owner
// carrying a blank reaches no caller, while the empty value held by the column
// default is no failure.
func TestReadRefusesAStoredOwnerOutsideTheDomain(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants (id, name, status, owner_account) VALUES "+
		"('t_acme', 'acme', 1, 'acct 1')")
	if err := r.read(context.Background()); !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("reading a row with an owner outside the domain gave %v, want ErrInvalidTenant", err)
	}

	empty := rosterOver(t, insert("t_other", "other"))
	if err := empty.read(context.Background()); err != nil {
		t.Fatalf("reading a row with no owner gave %v, want it accepted", err)
	}
}
