package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

func insertRow(id, name string, status tenant.Status, source tenant.DisabledBy) string {
	return fmt.Sprintf("INSERT INTO tenants (id, name, status, disabled_by) VALUES ('%s', '%s', %d, %d)",
		id, name, int(status), int(source))
}

func requireRow(t *testing.T, r *roster, id string, want row) {
	t.Helper()
	got, ok := rowOf(t, r, id)
	if !ok {
		t.Fatalf("the table holds no row for %q, want %+v", id, want)
	}
	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	got.DeletedAt, want.DeletedAt = nil, nil
	if got != want {
		t.Fatalf("the row for %q is %+v, want %+v", id, got, want)
	}
}

func TestDeleteWritesTheTerminalStateAndKeepsTheName(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()

	if err := r.Delete(ctx, "t_acme"); err != nil {
		t.Fatalf("deleting a tenant: %v", err)
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusDeleted)})

	got, ok, err := r.Get(ctx, "t_acme")
	if err != nil || !ok {
		t.Fatalf("the roster no longer holds the tenant just deleted: found=%v err=%v", ok, err)
	}
	if got.Status != tenant.StatusDeleted {
		t.Fatalf("the roster serves the tenant as %s, want %s", got.Status, tenant.StatusDeleted)
	}
	if got.Name != "acme" {
		t.Fatalf("the deleted tenant's name is %q, want the %q it had", got.Name, "acme")
	}
	if got.DisabledBy != tenant.DisabledByOperator {
		t.Fatalf("the deleted tenant's disable source is %s, want the operator's", got.DisabledBy)
	}
}

func TestDeleteWritesTheDeletionInstantAndKeepsTheOwner(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants (id, name, status, owner_account) VALUES "+
		"('t_acme', 'acme', 1, 'acct-1')")
	before := time.Now().UTC().Add(-time.Minute)

	if err := r.Delete(context.Background(), "t_acme"); err != nil {
		t.Fatalf("deleting a tenant: %v", err)
	}

	held, ok := rowOf(t, r, "t_acme")
	if !ok {
		t.Fatal("the deleted tenant left the table")
	}
	if held.Status != int(tenant.StatusDeleted) {
		t.Errorf("the row reads status %d, want deleted", held.Status)
	}
	if held.OwnerAccount != "acct-1" || held.Name != "acme" || held.ID != "t_acme" {
		t.Errorf("the tombstone is %+v, want the owner, name and identifier kept", held)
	}
	if held.DeletedAt == nil {
		t.Fatal("the row carries no deletion instant, want the moment of Delete")
	}
	if held.DeletedAt.Before(before) {
		t.Errorf("the deletion instant is %v, want no earlier than %v", held.DeletedAt, before)
	}
	if _, offset := held.DeletedAt.Zone(); offset != 0 {
		t.Errorf("the deletion instant is %v, want UTC", held.DeletedAt)
	}

	got, ok, err := r.Get(context.Background(), "t_acme")
	if err != nil || !ok {
		t.Fatalf("the roster no longer holds the tenant just deleted: found=%v err=%v", ok, err)
	}
	if got.OwnerAccount != "acct-1" || got.DeletedAt == nil || !got.DeletedAt.Equal(*held.DeletedAt) {
		t.Errorf("the snapshot reads %+v, want the owner and instant the table holds", got)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
	reads := countFullReads(t, r)

	if err := r.Delete(context.Background(), "t_acme"); err != nil {
		t.Fatalf("deleting a tombstone again gave %v, want nil", err)
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusDeleted)})
	if got := reads.Load(); got != 0 {
		t.Errorf("deleting a tombstone again reread the whole roster %d times, want none", got)
	}
}

func TestDeleteRefusesAnIdentifierItCannotPlace(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()
	for name, id := range map[string]tenant.ID{
		"no row carries it":  "t_ghost",
		"empty":              "",
		"outside the domain": "t acme",
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.Delete(ctx, id); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("deleting %q gave %v, want ErrInvalidTenant", id, err)
			}
		})
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusActive)})
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused deletions left %d rows, want the 1 the table started with", got)
	}
}

func TestATombstoneRefusesEveryWrite(t *testing.T) {
	tombstone := row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusDeleted)}
	for name, write := range map[string]struct {
		call func(*roster) error
		want error
	}{
		"Put": {func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive})
		}, tenant.ErrTenantDeleted},
		"SetStatus disabled": {func(r *roster) error {
			return r.SetStatus(context.Background(), "t_acme", tenant.StatusDisabled)
		}, tenant.ErrTenantDeleted},
		"SetStatus active": {func(r *roster) error {
			return r.SetStatus(context.Background(), "t_acme", tenant.StatusActive)
		}, tenant.ErrTenantDeleted},
		"SetStatusByAccount disabled": {func(r *roster) error {
			return r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusDisabled)
		}, tenant.ErrTenantDeleted},
		"SetStatusByAccount active": {func(r *roster) error {
			return r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive)
		}, tenant.ErrTenantDeleted},
		"SetProfile": {func(r *roster) error {
			return r.SetProfile(context.Background(), "t_acme", tenant.Profile{})
		}, tenant.ErrTenantDeleted},
		"SetOwner": {func(r *roster) error {
			return r.SetOwner(context.Background(), "t_acme", "acct-2")
		}, tenant.ErrTenantDeleted},
		"Create": {func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive})
		}, tenant.ErrTenantExists},
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, insertRow(tombstone.ID, tombstone.Name, tenant.StatusDeleted, tenant.DisabledByOperator))
			if err := write.call(r); !errors.Is(err, write.want) {
				t.Fatalf("%s on a tombstone gave %v, want %v", name, err, write.want)
			}
			requireRow(t, r, tombstone.ID, tombstone)
		})
	}
}

func TestATombstoneKeepsItsIdentifierAndName(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
	ctx := context.Background()

	if err := r.Create(ctx, tenant.Tenant{ID: "t_acme", Name: "acme-again", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("registering a tombstone's identifier gave %v, want ErrTenantExists", err)
	}
	if err := r.Create(ctx, tenant.Tenant{ID: "t_other", Name: "acme", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrNameConflict) {
		t.Fatalf("registering a tombstone's name gave %v, want ErrNameConflict", err)
	}
	if err := r.Put(ctx, tenant.Tenant{ID: "t_other", Name: "acme", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrNameConflict) {
		t.Fatalf("putting a tombstone's name gave %v, want ErrNameConflict", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused writes left %d rows, want the 1 the table started with", got)
	}
}

func TestDeleteReportsAWriteThatCouldNotBeReread(t *testing.T) {
	r := rosterOver(t, insert("acme corp", "acme"), insert("globex", "globex"))
	err := r.Delete(context.Background(), "globex")
	if err == nil {
		t.Fatal("the delete reported success although the roster could not be reread")
	}
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("the failure is %v, want it to carry the sentinel of the read that refused the table", err)
	}
	held, ok := rowOf(t, r, "globex")
	if !ok || held.Status != int(tenant.StatusDeleted) {
		t.Fatalf("the table holds (%+v, %v), want globex written as deleted", held, ok)
	}
}

func TestDeleteKeepsAnAccountsDisableSource(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, tenant.DisabledByAccount))

	if err := r.Delete(context.Background(), "t_acme"); err != nil {
		t.Fatalf("deleting a tenant an account disabled: %v", err)
	}
	requireRow(t, r, "t_acme", row{
		ID:         "t_acme",
		Name:       "acme",
		Status:     int(tenant.StatusDeleted),
		DisabledBy: int(tenant.DisabledByAccount),
	})
}
