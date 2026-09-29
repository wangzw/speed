package catalog

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/tenant"
)

// The roster reads and writes its table over a handle the host opened itself
// with gorm.Open, as it does over the one the database module delivers: its
// statements take a connection of whatever pool the handle is on.
func TestTheRosterWorksOverAHandleTheHostOpened(t *testing.T) {
	handle, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "host.db")),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a handle of the host's own: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(handle) })
	r := rosterOn(t, handle, "sqlite", insert("t_acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading the roster over the host's handle: %v", err)
	}
	if err := r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("Put over the host's handle: %v", err)
	}
	if err := r.SetStatus(ctx, "t_new", tenant.StatusDisabled); err != nil {
		t.Fatalf("SetStatus over the host's handle: %v", err)
	}
	if _, err := r.Enter(ctx, "t_new"); !errors.Is(err, tenant.ErrTenantDisabled) {
		t.Fatalf("entering the tenant just disabled over the host's handle gave %v, want ErrTenantDisabled", err)
	}
	if got, ok, err := r.Get(ctx, "t_acme"); err != nil || !ok || got.Alias != "acme" {
		t.Fatalf("Get over the host's handle gave %+v, %v, %v, want the tenant read", got, ok, err)
	}
}

// A role that may not read the roster's table is refused by the database, and
// the refusal is no missing table: the table is there, and the fix
// ErrRosterMissing names — the migration set — would not repair it. A listing
// of the database's tables leaves out the ones the role holds no privilege on,
// so the question the roster asks is one a missing privilege does not change.
func TestAPermissionRefusedIsNotAMissingRosterOnPostgres(t *testing.T) {
	dsn := pgtest.Acquire(t)
	// The role is made before the table, so it holds no privilege on it.
	asRole := pgtest.DMLRole(t, dsn)
	owner := quiet(dbtest.Postgres(t, dsn, nil).DB())
	ddl, err := fs.ReadFile(migrationsFS, "postgres/0001_tenants.sql")
	if err != nil {
		t.Fatalf("reading the postgres migration: %v", err)
	}
	if err := owner.Exec(string(ddl)).Error; err != nil {
		t.Fatalf("creating the table as its owner: %v", err)
	}
	if err := owner.Exec(insert("t_acme", "acme")).Error; err != nil {
		t.Fatalf("writing a row as the owner: %v", err)
	}
	r := newRoster(settings{}, databaseOver{handle: quiet(dbtest.Postgres(t, asRole, nil).DB())})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"a read of the whole roster": func() error { return r.read(ctx) },
		"Put": func() error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"Create": func() error {
			return r.Create(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func() error { return r.SetStatus(ctx, "t_acme", tenant.StatusDisabled) },
		"Enter": func() error {
			// An empty snapshot refuses the tenant, so the entry reads its row.
			r.snap.Store(buildSnapshot(nil))
			_, err := r.Enter(ctx, "t_acme")
			return err
		},
	} {
		err := call()
		if errors.Is(err, tenant.ErrRosterMissing) {
			t.Errorf("%s by a role with no privilege on the table gave %v, want no ErrRosterMissing", name, err)
		}
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s by a role with no privilege on the table gave %v, want the database's refusal", name, err)
		}
	}
}
