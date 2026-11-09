package catalog

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases here hold the transaction-aware roster writes: they write in the
// transaction they are handed, they return the reread for after its commit, and
// they refuse what the context-taking writes refuse.

// txAfter is one after closure a tx write handed back.
type txAfter func(context.Context) error

func TestCreateTxWritesInTheCallersTransaction(t *testing.T) {
	r := rosterOver(t)
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	want := tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	rollback := errors.New("caller rolled back")
	var after txAfter
	err := r.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = r.CreateTx(context.Background(), tx, want)
		if err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("the caller's transaction returned %v, want the rollback", err)
	}
	if after == nil {
		t.Fatal("CreateTx wrote a row and returned no after closure")
	}
	if _, ok := rowOf(t, r, "t_acme"); ok {
		t.Error("the rolled-back creation left a row in the table")
	}
}

func TestCreateTxRereadsTheRosterOnlyAfterTheCallersCommit(t *testing.T) {
	r := rosterOver(t)
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	want := tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	var after txAfter
	if err := r.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = r.CreateTx(context.Background(), tx, want)
		return err
	}); err != nil {
		t.Fatalf("CreateTx: %v", err)
	}
	if _, ok, _ := r.Get(context.Background(), "t_acme"); ok {
		t.Fatal("the roster already serves the tenant before the after closure ran")
	}
	if err := after(context.Background()); err != nil {
		t.Fatalf("running the after closure: %v", err)
	}
	got, ok, err := r.Get(context.Background(), "t_acme")
	if err != nil || !ok || got.Name != want.Name || got.Status != want.Status {
		t.Fatalf("the roster serves (%+v, %v, %v) after the after closure, want the created tenant", got, ok, err)
	}
}

func TestDeleteTxWritesInTheCallersTransaction(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	rollback := errors.New("caller rolled back")
	if err := r.handle.Transaction(func(tx *gorm.DB) error {
		if _, err := r.DeleteTx(context.Background(), tx, "t_acme"); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("the caller's transaction returned %v, want the rollback", err)
	}
	held, ok := rowOf(t, r, "t_acme")
	if !ok || tenant.Status(held.Status) == tenant.StatusDeleted {
		t.Fatalf("the rolled-back deletion left the row as %+v, want it live", held)
	}
}

func TestDeleteTxRereadsTheRosterOnlyAfterTheCallersCommit(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	var after txAfter
	if err := r.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = r.DeleteTx(context.Background(), tx, "t_acme")
		return err
	}); err != nil {
		t.Fatalf("DeleteTx: %v", err)
	}
	if got, ok, _ := r.Get(context.Background(), "t_acme"); !ok || got.Status != tenant.StatusActive {
		t.Fatalf("the roster reads (%+v, %v) before the after closure, want the tenant still active", got, ok)
	}
	if err := after(context.Background()); err != nil {
		t.Fatalf("running the after closure: %v", err)
	}
	if got, ok, _ := r.Get(context.Background(), "t_acme"); !ok || got.Status != tenant.StatusDeleted {
		t.Fatalf("the roster reads (%+v, %v) after the after closure, want the tombstone", got, ok)
	}
	held, ok := rowOf(t, r, "t_acme")
	if !ok || tenant.Status(held.Status) != tenant.StatusDeleted {
		t.Fatalf("the committed deletion left the row as %+v, want a tombstone", held)
	}
}

func TestTheTxVariantsKeepTheCtxVariantsRefusals(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if err := r.handle.Transaction(func(tx *gorm.DB) error {
		if _, err := r.CreateTx(context.Background(), tx,
			tenant.Tenant{ID: "t_acme", Name: "other", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrTenantExists) {
			t.Errorf("CreateTx of an identifier the table holds gave %v, want ErrTenantExists", err)
		}
		if _, err := r.DeleteTx(context.Background(), tx, "t_missing"); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Errorf("DeleteTx of an identifier no row carries gave %v, want ErrInvalidTenant", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("driving the refusals: %v", err)
	}
}

// GetTx reports a row no table carries as absent and a tombstone as present, so
// a caller that has to distinguish the two reads the status rather than the
// boolean.
func TestGetTxReportsAbsenceAndTheTombstone(t *testing.T) {
	r := rosterOver(t, insertRow("t_gone", "gone", tenant.StatusDeleted, tenant.DisabledByOperator))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if err := r.handle.Transaction(func(tx *gorm.DB) error {
		if _, found, err := r.GetTx(context.Background(), tx, "t_missing"); err != nil || found {
			t.Errorf("GetTx of an identifier no row carries = (found %t, %v), want (false, nil)", found, err)
		}
		got, found, err := r.GetTx(context.Background(), tx, "t_gone")
		if err != nil || !found {
			t.Fatalf("GetTx of a tombstone = (found %t, %v), want (true, nil)", found, err)
		}
		if got.Status != tenant.StatusDeleted {
			t.Errorf("GetTx of a tombstone reports status %q, want deleted", got.Status)
		}
		return nil
	}); err != nil {
		t.Fatalf("driving GetTx: %v", err)
	}
}

// SetOwnerTx refuses an identifier no row carries and a tombstone, writes
// nothing for a transfer to the current owner, and answers no error for an
// update that matched no row but left a live row behind.
func TestSetOwnerTxRejectionBranches(t *testing.T) {
	r := rosterOver(t,
		insertOwner("t_acme", "acme", "alice"),
		insertOwner("t_gone", "gone", ""),
		insertRow("t_tomb", "tomb", tenant.StatusDeleted, tenant.DisabledByOperator),
	)
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}

	t.Run("no roster row", func(t *testing.T) {
		if err := r.handle.Transaction(func(tx *gorm.DB) error {
			if _, err := r.SetOwnerTx(context.Background(), tx, "t_missing", "bob"); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Errorf("SetOwnerTx of an identifier no row carries gave %v, want ErrInvalidTenant", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("driving SetOwnerTx: %v", err)
		}
	})

	t.Run("tombstone", func(t *testing.T) {
		if err := r.handle.Transaction(func(tx *gorm.DB) error {
			if _, err := r.SetOwnerTx(context.Background(), tx, "t_tomb", "bob"); !errors.Is(err, tenant.ErrTenantDeleted) {
				t.Errorf("SetOwnerTx of a tombstone gave %v, want ErrTenantDeleted", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("driving SetOwnerTx: %v", err)
		}
	})

	t.Run("same owner", func(t *testing.T) {
		if err := r.handle.Transaction(func(tx *gorm.DB) error {
			after, err := r.SetOwnerTx(context.Background(), tx, "t_acme", "alice")
			if err != nil {
				t.Fatalf("SetOwnerTx to the current owner: %v", err)
			}
			if after != nil {
				t.Error("SetOwnerTx to the current owner returned an after closure, want none")
			}
			return nil
		}); err != nil {
			t.Fatalf("driving SetOwnerTx: %v", err)
		}
	})

	t.Run("an update that changed nothing", func(t *testing.T) {
		if err := r.handle.Exec(`CREATE TRIGGER ignore_owner_write
			BEFORE UPDATE OF owner_account ON tenants
			BEGIN SELECT RAISE(IGNORE); END`).Error; err != nil {
			t.Fatalf("creating the trigger that ignores the update: %v", err)
		}
		t.Cleanup(func() {
			if err := r.handle.Exec("DROP TRIGGER ignore_owner_write").Error; err != nil {
				t.Fatalf("dropping the trigger: %v", err)
			}
		})
		if err := r.handle.Transaction(func(tx *gorm.DB) error {
			after, err := r.SetOwnerTx(context.Background(), tx, "t_acme", "bob")
			if err != nil {
				t.Fatalf("SetOwnerTx whose update matched no row: %v", err)
			}
			if after != nil {
				t.Error("SetOwnerTx whose update matched no row returned an after closure, want none")
			}
			return nil
		}); err != nil {
			t.Fatalf("driving SetOwnerTx: %v", err)
		}
	})
}

// insertOwner is one roster row written as a statement with its owner column,
// which insert does not carry.
func insertOwner(id, name, owner string) string {
	return "INSERT INTO tenants (id, name, status, owner_account) VALUES ('" +
		id + "', '" + name + "', 1, '" + owner + "')"
}
