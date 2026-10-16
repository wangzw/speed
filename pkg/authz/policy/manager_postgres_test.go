//go:build integration

package policy

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

// The cases here need a real, multi-connection server. SQLite serialises its
// writers on one global lock, so the interleaving that leaves an orphan row —
// a write's role check landing before a concurrent delete commits — cannot be
// staged on it. On PostgreSQL it can, deterministically: the delete is held open
// on its own connection and the write is watched for the one thing that decides
// the outcome, whether its check waits on the role the delete has locked.

// newPostgresFixture is newFixture over the shared PostgreSQL server: the same
// tables the module ships, built from the PostgreSQL migration file, with the
// tenant filter installed.
func newPostgresFixture(t *testing.T) *fixture {
	t.Helper()
	handle := dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
	if err := handle.Use(tenantFilter(t)); err != nil {
		t.Fatalf("installing the tenant filter: %v", err)
	}
	applyMigration(t, handle, "postgres/0001_authz_policy.sql")
	reg := core.New()
	reg.Register(Module())
	p := &product{
		handle:      handle,
		text:        authz.ModelText,
		loadTimeout: 5 * time.Second,
		catalog:     buildCatalog(reg),
		logger:      log.Default(),
	}
	if err := p.begin(t.Context()); err != nil {
		t.Fatalf("starting the product: %v", err)
	}
	return &fixture{product: p}
}

const (
	// lockWaitBudget bounds the wait for the write to reach the decisive state.
	// The write reaches the role lock in the microseconds of one statement; the
	// budget only stops the case hanging if it never does, and a case that saw
	// neither completion nor the wait fails rather than passes.
	lockWaitBudget = 10 * time.Second
	// pollInterval is how often the case looks, the pace pollUntil also uses.
	pollInterval = 2 * time.Millisecond
)

// holdDeleteOfRole opens a transaction that deletes the role's rows and leaves
// it uncommitted, so the role row is locked for the duration. It stands for a
// DeleteRole in flight: the delete takes the role row's exclusive lock on its
// way to removing it, and this case keeps the transaction open at that point.
func holdDeleteOfRole(t *testing.T, f *fixture, tenant, role string) *gorm.DB {
	t.Helper()
	tx := f.crossing(t.Context()).Begin()
	if tx.Error != nil {
		t.Fatalf("beginning the concurrent delete: %v", tx.Error)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"DELETE FROM authz_grants WHERE tenant_id = ? AND role = ?", []any{tenant, role}},
		{"DELETE FROM authz_bindings WHERE tenant_id = ? AND role = ?", []any{tenant, role}},
		{"DELETE FROM authz_roles WHERE tenant_id = ? AND role = ?", []any{tenant, role}},
	} {
		if err := tx.Exec(statement.sql, statement.args...).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("running the concurrent delete: %v", err)
		}
	}
	return tx
}

// aWriteIsWaitingForALock reports whether a session on this database is blocked
// on a lock. Only this case's connections exist on the database it runs on, so
// the only thing that can be waiting is the write, held on the role the
// concurrent delete locked.
func aWriteIsWaitingForALock(t *testing.T, f *fixture) bool {
	t.Helper()
	var waiting int64
	err := f.crossing(t.Context()).Raw(
		"SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'",
	).Scan(&waiting).Error
	if err != nil {
		t.Fatalf("reading the waiting sessions: %v", err)
	}
	return waiting > 0
}

// theWriteReachedTheLock drives the write to the one of two states that decides
// the case. It reports held=true once the write is seen waiting on the role lock
// (its check took the lock) and held=false with the write's error once the write
// completes first (its check took no lock). It fails if neither is reached
// within the budget, so a write neither done nor seen waiting cannot pass as
// correct.
func theWriteReachedTheLock(t *testing.T, f *fixture, done <-chan error) (err error, held bool) {
	t.Helper()
	deadline := time.Now().Add(lockWaitBudget)
	for {
		select {
		case err := <-done:
			return err, false
		default:
		}
		if aWriteIsWaitingForALock(t, f) {
			return nil, true
		}
		if time.Now().After(deadline) {
			t.Fatalf("the write neither completed nor was seen waiting on the role lock within %s", lockWaitBudget)
		}
		time.Sleep(pollInterval)
	}
}

// leftoversOf counts the rows of table still naming a role, the orphan the
// engine would go on deciding with.
func leftoversOf(t *testing.T, f *fixture, table, tenant, role string) int64 {
	t.Helper()
	var count int64
	if err := f.crossing(t.Context()).
		Raw("SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ? AND role = ?", tenant, role).
		Scan(&count).Error; err != nil {
		t.Fatalf("reading %s back: %v", table, err)
	}
	return count
}

// The check a grant write makes against its role must wait out a concurrent
// delete of that role, so the grant cannot land on a role the delete removed.
//
// With the check taking the role's row lock it waits until the delete commits
// and then reads the role as gone, refusing the grant with ErrUnknownRole; the
// write's transaction ends with no row written, so nothing the engine decides
// with outlives the role. A check that took no lock reads the still-present
// (uncommitted-deleted) role as existing and writes the grant, which the delete
// leaves behind — a grant the engine keeps deciding with for a role that no
// longer exists.
func TestPutGrantWaitsForAConcurrentDeleteOfItsRole(t *testing.T) {
	f := newPostgresFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "race", "roleA")

	control := holdDeleteOfRole(t, f, "race", "roleA")

	done := make(chan error, 1)
	go func() {
		done <- f.PutGrant(ctx, authz.GrantRecord{
			Tenant: "race", Role: "roleA", Resource: "account", Action: "read",
		})
	}()

	if err, held := theWriteReachedTheLock(t, f, done); !held {
		_ = control.Rollback()
		t.Fatalf("the grant was written while a delete had the role locked (PutGrant returned %v): "+
			"the check took no row lock, so the grant outlives the role the delete removes", err)
	}

	if err := control.Commit().Error; err != nil {
		t.Fatalf("committing the concurrent delete: %v", err)
	}
	if err := <-done; !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("the grant on a role the delete removed reported %v, want ErrUnknownRole", err)
	}
	if leftover := leftoversOf(t, f, "authz_grants", "race", "roleA"); leftover != 0 {
		t.Errorf("the role's delete left %d grant row(s) behind: the engine decides from authz_grants "+
			"alone, so a leftover grant keeps deciding for a role that is gone", leftover)
	}
}

// TestPutBindingWaitsForAConcurrentDeleteOfItsRole is the binding half of the
// same guarantee: a binding must not land on a role a concurrent delete removed.
func TestPutBindingWaitsForAConcurrentDeleteOfItsRole(t *testing.T) {
	f := newPostgresFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "race", "roleA")

	control := holdDeleteOfRole(t, f, "race", "roleA")

	done := make(chan error, 1)
	go func() {
		done <- f.PutBinding(ctx, authz.Binding{Tenant: "race", Account: "acc1", Role: "roleA"})
	}()

	if err, held := theWriteReachedTheLock(t, f, done); !held {
		_ = control.Rollback()
		t.Fatalf("the binding was written while a delete had the role locked (PutBinding returned %v): "+
			"the check took no row lock, so the binding outlives the role the delete removes", err)
	}

	if err := control.Commit().Error; err != nil {
		t.Fatalf("committing the concurrent delete: %v", err)
	}
	if err := <-done; !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("the binding on a role the delete removed reported %v, want ErrUnknownRole", err)
	}
	if leftover := leftoversOf(t, f, "authz_bindings", "race", "roleA"); leftover != 0 {
		t.Errorf("the role's delete left %d binding row(s) behind: the engine decides from authz_bindings "+
			"alone, so a leftover binding keeps deciding for a role that is gone", leftover)
	}
}
