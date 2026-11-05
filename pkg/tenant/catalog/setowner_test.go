package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about the owner write: SetOwner changes the owner
// column and nothing else, refuses a tombstone and a value outside the domain,
// and leaves a repeated transfer as a no-op. Create is where an owner enters the
// table, and Put is not a transfer path.

// TestSetOwnerWritesOnlyTheOwnerColumn holds the statement to one column. The
// alias, the state, the disable source, the profile and both instants are what
// other paths own, and a transfer must not write them back from a value it read
// earlier.
func TestSetOwnerWritesOnlyTheOwnerColumn(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants "+
		"(id, alias, status, disabled_by, owner_account, display_name, description) VALUES "+
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
	if after.ID != before.ID || after.Alias != before.Alias || after.Status != before.Status ||
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

// TestSetOwnerRefusesATombstoneAndAnUnknownTenant holds the two refusals apart:
// a deleted row keeps its owner, and an identifier no row carries is the
// caller's mistake. Neither writes anything.
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

// TestSetOwnerRefusesAnOwnerOutsideTheDomain holds the value's shape before the
// table is touched: a business organization always has an owner, so the empty
// value is refused, as a blank or control character is.
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

// TestSetOwnerIsIdempotent holds the repeated transfer: writing the owner a row
// already carries returns nil and pays for no reread.
func TestSetOwnerIsIdempotent(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants (id, alias, status, owner_account) VALUES "+
		"('t_acme', 'acme', 1, 'acct-1')")
	reads := countFullReads(t, r)

	if err := r.SetOwner(context.Background(), "t_acme", "acct-1"); err != nil {
		t.Fatalf("repeating a transfer gave %v, want nil", err)
	}
	if got := reads.Load(); got != 0 {
		t.Errorf("a repeated transfer reread the whole roster %d times, want none", got)
	}
}

// TestCreateWritesTheOwnerAndPutLeavesItAlone holds the creation path and the
// rewrite path apart: Create writes the value's owner, and a rewrite through Put
// changes the alias, the state and the disable source without transferring the
// organization.
func TestCreateWritesTheOwnerAndPutLeavesItAlone(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()

	if err := r.Create(ctx, tenant.Tenant{
		ID: "t_acme", Alias: "acme", Status: tenant.StatusActive, OwnerAccount: "acct-1",
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

	if err := r.Put(ctx, tenant.Tenant{
		ID: "t_acme", Alias: "acme-two", Status: tenant.StatusDisabled, OwnerAccount: "acct-2",
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
	if rewritten.Alias != "acme-two" || rewritten.Status != tenant.StatusDisabled {
		t.Errorf("the rewrite left %+v, want the new alias and state", rewritten)
	}
}

// TestReadRefusesAStoredOwnerOutsideTheDomain holds the read path: the table can
// be written without SetOwner, and an owner value carrying a blank reaches no
// caller. The empty value is the one the column's default holds and is no
// failure.
func TestReadRefusesAStoredOwnerOutsideTheDomain(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants (id, alias, status, owner_account) VALUES "+
		"('t_acme', 'acme', 1, 'acct 1')")
	if err := r.read(context.Background()); !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("reading a row with an owner outside the domain gave %v, want ErrInvalidTenant", err)
	}

	empty := rosterOver(t, insert("t_other", "other"))
	if err := empty.read(context.Background()); err != nil {
		t.Fatalf("reading a row with no owner gave %v, want it accepted", err)
	}
}
