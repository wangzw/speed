package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about deletion: the one write that reaches the
// terminal state, and what every other write and entry owes a tombstone.

// insertRow is one roster row written as a statement, with its status and its
// disable source given. It is the route to the table Put and Create do not
// police — a tombstone, and a row whose source this build does not know, reach
// the roster only this way.
func insertRow(id, alias string, status tenant.Status, source tenant.DisabledBy) string {
	return fmt.Sprintf("INSERT INTO tenants (id, alias, status, disabled_by) VALUES ('%s', '%s', %d, %d)",
		id, alias, int(status), int(source))
}

// requireRow asserts that one roster row holds exactly these values, read back
// from the table rather than from the snapshot. The creation and deletion
// instants are the roster's to write and vary by call, so they are cleared on
// both sides; a case that cares about one reads it directly.
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

// Delete writes the terminal state and changes nothing else: the row, the
// identifier and the alias stay, so a scoped table's data still has a tenant to
// be found under, and a caller reading the roster sees what became of it.
func TestDeleteWritesTheTerminalStateAndKeepsTheName(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()

	if err := r.Delete(ctx, "t_acme"); err != nil {
		t.Fatalf("deleting a tenant: %v", err)
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusDeleted)})

	got, ok, err := r.Get(ctx, "t_acme")
	if err != nil || !ok {
		t.Fatalf("the roster no longer holds the tenant just deleted: found=%v err=%v", ok, err)
	}
	if got.Status != tenant.StatusDeleted {
		t.Fatalf("the roster serves the tenant as %s, want %s", got.Status, tenant.StatusDeleted)
	}
	if got.Alias != "acme" {
		t.Fatalf("the deleted tenant's alias is %q, want the %q it had", got.Alias, "acme")
	}
	if got.DisabledBy != tenant.DisabledByOperator {
		t.Fatalf("the deleted tenant's disable source is %s, want the operator's", got.DisabledBy)
	}
}

// Delete records the instant of the terminal write beside the state, and keeps
// the identifier, the alias and the owner: the tombstone is the row it was,
// marked rather than emptied.
func TestDeleteWritesTheDeletionInstantAndKeepsTheOwner(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants (id, alias, status, owner_account) VALUES "+
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
	if held.OwnerAccount != "acct-1" || held.Alias != "acme" || held.ID != "t_acme" {
		t.Errorf("the tombstone is %+v, want the owner, alias and identifier kept", held)
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

// Deleting a tombstone again is a no-op that succeeds and writes nothing: a
// sweep that retries a deletion has no way to know it already landed, and a
// failure would leave it retrying for good. The reread after a write serves the
// writer its own write, so a no-op must not pay for one.
func TestDeleteIsIdempotent(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
	reads := countFullReads(t, r)

	if err := r.Delete(context.Background(), "t_acme"); err != nil {
		t.Fatalf("deleting a tombstone again gave %v, want nil", err)
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusDeleted)})
	if got := reads.Load(); got != 0 {
		t.Errorf("deleting a tombstone again reread the whole roster %d times, want none", got)
	}
}

// An identifier no row carries, and one outside the domain, are refused and
// nothing is written: there is no row to write, and a value the domain refuses
// is no tenant's.
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
	requireRow(t, r, "t_acme", row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusActive)})
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused deletions left %d rows, want the 1 the table started with", got)
	}
}

// A tombstone is terminal for every write path but Create, which answers with
// the registration conflict it answers every existing identifier with. Each
// call is checked against the table as well as its error: a write that returns
// ErrTenantDeleted after changing the row is not the refusal the sentinel
// claims.
func TestATombstoneRefusesEveryWrite(t *testing.T) {
	tombstone := row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusDeleted)}
	for name, write := range map[string]struct {
		call func(*roster) error
		want error
	}{
		"Put": {func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive})
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
			return r.Create(context.Background(), tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive})
		}, tenant.ErrTenantExists},
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, insertRow(tombstone.ID, tombstone.Alias, tenant.StatusDeleted, tenant.DisabledByOperator))
			if err := write.call(r); !errors.Is(err, write.want) {
				t.Fatalf("%s on a tombstone gave %v, want %v", name, err, write.want)
			}
			requireRow(t, r, tombstone.ID, tombstone)
		})
	}
}

// A tombstone keeps its identifier and its alias: the identifier cannot be
// registered again, and the alias cannot be put on another tenant while the row
// still carries it.
func TestATombstoneKeepsItsIdentifierAndAlias(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
	ctx := context.Background()

	if err := r.Create(ctx, tenant.Tenant{ID: "t_acme", Alias: "acme-again", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("registering a tombstone's identifier gave %v, want ErrTenantExists", err)
	}
	if err := r.Create(ctx, tenant.Tenant{ID: "t_other", Alias: "acme", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrAliasConflict) {
		t.Fatalf("registering a tombstone's alias gave %v, want ErrAliasConflict", err)
	}
	if err := r.Put(ctx, tenant.Tenant{ID: "t_other", Alias: "acme", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrAliasConflict) {
		t.Fatalf("putting a tombstone's alias gave %v, want ErrAliasConflict", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused writes left %d rows, want the 1 the table started with", got)
	}
}

// A delete that cannot reread the roster afterwards reports the write as
// applied, as every other writer does: the deletion is durable and this process
// is the only one that can say its snapshot does not hold it.
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

// Delete writes the status column and nothing else. The disable source is part
// of "nothing else": a row an account disabled keeps that source through the
// deletion, read back from the raw column rather than through the normalising
// accessor. A delete that rewrote the source as the operator's would otherwise
// be indistinguishable from one that preserved it, because the operator's value
// is the column's default.
func TestDeleteKeepsAnAccountsDisableSource(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, tenant.DisabledByAccount))

	if err := r.Delete(context.Background(), "t_acme"); err != nil {
		t.Fatalf("deleting a tenant an account disabled: %v", err)
	}
	requireRow(t, r, "t_acme", row{
		ID:         "t_acme",
		Alias:      "acme",
		Status:     int(tenant.StatusDeleted),
		DisabledBy: int(tenant.DisabledByAccount),
	})
}
