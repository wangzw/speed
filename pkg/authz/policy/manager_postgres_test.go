//go:build integration

package policy

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

func newPostgresFixture(t *testing.T) *fixture {
	t.Helper()
	handle := dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
	if err := handle.Use(tenantFilter(t)); err != nil {
		t.Fatalf("installing the tenant filter: %v", err)
	}
	applyMigration(t, handle, "postgres/0001_authz_policy.sql")
	applyMigration(t, handle, "postgres/0003_role_description.sql")
	reg := core.New()
	reg.Register(Module())
	p := &product{
		handle:      handle,
		dialect:     db.Postgres,
		text:        authz.ModelText,
		loadTimeout: 5 * time.Second,
		catalog:     mustBuildCatalog(t, reg),
		logger:      log.Default(),
		tenantLock:  authz.NewInProcessTenantLock(),
	}
	if err := p.begin(t.Context()); err != nil {
		t.Fatalf("starting the product: %v", err)
	}
	return &fixture{product: p}
}

const (
	lockWaitBudget = 10 * time.Second
	pollInterval   = 2 * time.Millisecond
)

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

// TestPutGrantWaitsForAConcurrentDeleteOfItsRole checks that put grant waits for a concurrent delete of its role.
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

// TestPutBindingWaitsForAConcurrentDeleteOfItsRole checks that put binding waits for a concurrent delete of its role.
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
