//go:build integration

package catalog

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/tenant"
)

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

// rosterCalls is every call of the roster's that reads or writes its table,
// the entry reading the row an empty snapshot refuses.
func rosterCalls(ctx context.Context, r *roster) map[string]func() error {
	return map[string]func() error{
		"a read of the whole roster": func() error { return r.read(ctx) },
		"Put": func() error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"Create": func() error {
			return r.Create(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func() error { return r.SetStatus(ctx, "t_acme", tenant.StatusDisabled) },
		"Enter": func() error {
			r.snap.Store(buildSnapshot(nil))
			_, err := r.Enter(ctx, "t_acme")
			return err
		},
	}
}

// roleOf is the role a locator logs in as, quoted as an identifier.
func roleOf(t *testing.T, dsn string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("reading the locator: %v", err)
	}
	return pgx.Identifier{parsed.User.Username()}.Sanitize()
}

// createRosterTable creates the roster's table from the migration the module
// ships for PostgreSQL, over a handle, and writes acme's row into it.
func createRosterTable(t *testing.T, handle *gorm.DB) {
	t.Helper()
	ddl, err := fs.ReadFile(migrationsFS, "postgres/0001_tenants.sql")
	if err != nil {
		t.Fatalf("reading the postgres migration: %v", err)
	}
	for _, stmt := range []string{string(ddl), insert("t_acme", "acme")} {
		if err := handle.Exec(stmt).Error; err != nil {
			t.Fatalf("creating the roster table: %v", err)
		}
	}
}

// A role that may use the roster's table but not the schema it is in fails
// every statement as if the table were not there: PostgreSQL drops a schema the
// role holds no USAGE on from the path it searches. The table is there all the
// same, and the migration ErrRosterMissing names would not repair it, so the
// failure is reported as it stands.
func TestAMissingSchemaPrivilegeIsNotAMissingRosterOnPostgres(t *testing.T) {
	dsn := pgtest.Acquire(t)
	asRole := pgtest.DMLRole(t, dsn)
	owner := quiet(dbtest.Postgres(t, dsn, nil).DB())
	createRosterTable(t, owner)
	role := roleOf(t, asRole)
	for _, stmt := range []string{
		"GRANT SELECT, INSERT, UPDATE, DELETE ON tenants TO " + role,
		"REVOKE USAGE ON SCHEMA public FROM PUBLIC",
		"REVOKE USAGE ON SCHEMA public FROM " + role,
	} {
		if err := owner.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	r := newRoster(settings{}, databaseOver{handle: quiet(dbtest.Postgres(t, asRole, nil).DB())})
	for name, call := range rosterCalls(context.Background(), r) {
		err := call()
		if err == nil || errors.Is(err, tenant.ErrRosterMissing) {
			t.Errorf("%s by a role with no USAGE on the table's schema gave %v, want the database's failure "+
				"and no ErrRosterMissing", name, err)
		}
	}
}

// The roster's table is looked for where the session's search path names, as
// its statements look for it: a table in a schema the path names other than
// public is there, so a refused permission on it is no missing table; and a
// table only in a schema the path does not name is not where the statements
// look, so the roster is missing — the migration, run over the same locator,
// creates it where they look.
func TestTheRosterIsLookedForWhereTheSearchPathNamesOnPostgres(t *testing.T) {
	t.Run("a table on a path other than public", func(t *testing.T) {
		dsn := pgtest.Acquire(t)
		asRole := pgtest.DMLRole(t, dsn)
		owner := quiet(dbtest.Postgres(t, dsn, nil).DB())
		for _, stmt := range []string{
			"CREATE SCHEMA elsewhere",
			"GRANT USAGE ON SCHEMA elsewhere TO " + roleOf(t, asRole),
		} {
			if err := owner.Exec(stmt).Error; err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		createRosterTable(t, quiet(dbtest.Postgres(t, dsn+"&search_path=elsewhere", nil).DB()))
		r := newRoster(settings{}, databaseOver{
			handle: quiet(dbtest.Postgres(t, asRole+"&search_path=elsewhere", nil).DB()),
		})
		for name, call := range rosterCalls(context.Background(), r) {
			err := call()
			if errors.Is(err, tenant.ErrRosterMissing) {
				t.Errorf("%s by a role with no privilege on the table its path reaches gave %v, want no "+
					"ErrRosterMissing", name, err)
			}
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Errorf("%s by a role with no privilege on the table its path reaches gave %v, want the "+
					"database's refusal", name, err)
			}
		}
	})
	t.Run("a table off the path", func(t *testing.T) {
		dsn := pgtest.Acquire(t)
		owner := quiet(dbtest.Postgres(t, dsn, nil).DB())
		if err := owner.Exec("CREATE SCHEMA aside").Error; err != nil {
			t.Fatalf("creating the other schema: %v", err)
		}
		createRosterTable(t, quiet(dbtest.Postgres(t, dsn+"&search_path=aside", nil).DB()))
		r := newRoster(settings{}, databaseOver{handle: owner})
		for name, call := range rosterCalls(context.Background(), r) {
			if err := call(); !errors.Is(err, tenant.ErrRosterMissing) {
				t.Errorf("%s over a path not naming the table's schema gave %v, want ErrRosterMissing", name, err)
			}
		}
	})
}

// A statement of the roster's whose connection is lost fails for a reason no
// migration repairs, and the question that would tell a missing table fails on
// the same connection: nothing then says the table is not there, and the
// failure is reported as it stands.
func TestALostConnectionIsNotAMissingRosterOnPostgres(t *testing.T) {
	r := rosterOverPostgres(t)
	if err := r.handle.Exec(insert("t_acme", "acme")).Error; err != nil {
		t.Fatalf("writing a row: %v", err)
	}
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"a read of the whole roster": func() error { return r.read(ctx) },
		"Put": func() error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
	} {
		t.Run(name, func(t *testing.T) {
			holder := r.handle.Begin()
			defer holder.Rollback()
			if err := holder.Exec("LOCK TABLE " + tableName + " IN ACCESS EXCLUSIVE MODE").Error; err != nil {
				t.Fatalf("locking the table: %v", err)
			}
			done := make(chan error, 1)
			go func() { done <- call() }()
			terminateWaiter(t, r.handle, done)
			err := <-done
			if err == nil || errors.Is(err, tenant.ErrRosterMissing) {
				t.Errorf("%s whose connection was terminated gave %v, want the failure as it stands", name, err)
			}
		})
	}
}

// terminateWaiter ends the session of the one statement waiting for a lock in
// the handle's database, once it is waiting.
func terminateWaiter(t *testing.T, handle *gorm.DB, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("the call returned %v before it waited for the lock", err)
		default:
		}
		var pids []int
		if err := handle.Raw(`SELECT pid FROM pg_catalog.pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).
			Scan(&pids).Error; err != nil {
			t.Fatalf("finding the waiting session: %v", err)
		}
		if len(pids) == 1 {
			if err := handle.Exec("SELECT pg_terminate_backend(?)", pids[0]).Error; err != nil {
				t.Fatalf("terminating the waiting session: %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no statement came to wait for the lock")
}

// The search path is read as PostgreSQL reads it: for each setting, the names
// read from it that are schemas of the database are the path PostgreSQL itself
// searches, in order. A schema named after the session's role is there, so
// that "$user" stands for something.
func TestASearchPathIsReadAsPostgreSQLReadsIt(t *testing.T) {
	dsn := pgtest.Acquire(t)
	owner := quiet(dbtest.Postgres(t, dsn, nil).DB())
	for _, stmt := range []string{
		`CREATE SCHEMA plain`, `CREATE SCHEMA "Mixed"`, `CREATE SCHEMA "q""uote"`, `CREATE SCHEMA "with, comma"`,
		"CREATE SCHEMA " + roleOf(t, dsn),
	} {
		if err := owner.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for _, setting := range []string{
		`plain`, `PLAIN`, ` "Mixed" ,plain`, `"q""uote", public`, `"$user", "with, comma", public`,
		`$user,Plain`, `mixed, "Mixed"`,
	} {
		t.Run(setting, func(t *testing.T) {
			// The driver reads a plus in the locator's query as itself, so a
			// blank is escaped as a percent sequence.
			escaped := strings.ReplaceAll(url.QueryEscape(setting), "+", "%20")
			session := quiet(dbtest.Postgres(t, dsn+"&search_path="+escaped, nil).DB())
			var effective []string
			if err := session.Raw(`SELECT unnest(pg_catalog.current_schemas(false))`).
				Scan(&effective).Error; err != nil {
				t.Fatalf("reading the path PostgreSQL searches: %v", err)
			}
			named, err := namedSchemas(session)
			if err != nil {
				t.Fatalf("reading the path's names: %v", err)
			}
			var existing []string
			if err := session.Raw(`SELECT nspname FROM pg_catalog.pg_namespace`).Scan(&existing).Error; err != nil {
				t.Fatalf("listing the schemas: %v", err)
			}
			var read []string
			for _, name := range named {
				if slices.Contains(existing, name) {
					read = append(read, name)
				}
			}
			if !slices.Equal(read, effective) {
				t.Errorf("search_path %q reads as %q, of which the schemas are %q; PostgreSQL searches %q",
					setting, named, read, effective)
			}
		})
	}
}
