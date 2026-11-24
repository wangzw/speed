//go:build integration

package postgres_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestARoleWithoutDDLPrivilegeStartsOnAnAppliedDatabase pins the deployment that splits the
// credentials: migrations applied by a role that owns the schema.
func TestARoleWithoutDDLPrivilegeStartsOnAnAppliedDatabase(t *testing.T) {
	dsn := pgtest.Acquire(t)
	if _, err := runAssembly(t, dsn); err != nil {
		t.Fatalf("applying the migrations as the owner: %v", err)
	}

	owner := open(t, dsn)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("reading the fixture's locator: %v", err)
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	role := fmt.Sprintf("dml_only_%d_%d", os.Getpid(), time.Now().UnixNano())
	for _, statement := range []string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'dml-password'`, role),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %s TO %s`, database, role),
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, role),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, role),
	} {
		if execErr := owner.Exec(statement).Error; execErr != nil {
			t.Fatalf("setting up the role: %s: %v", statement, execErr)
		}
	}
	t.Cleanup(func() {
		for _, statement := range []string{
			fmt.Sprintf(`DROP OWNED BY %s`, role),
			fmt.Sprintf(`DROP ROLE %s`, role),
		} {
			if execErr := owner.Exec(statement).Error; execErr != nil {
				t.Errorf("removing the role: %s: %v", statement, execErr)
			}
		}
	})
	parsed.User = url.UserPassword(role, "dml-password")
	roleDSN := parsed.String()

	// The control: the role really cannot create anything, so a startup that succeeds below is one that asked for nothing it lacks.
	asRole, err := sql.Open("pgx", roleDSN)
	if err != nil {
		t.Fatalf("opening a connection as the role: %v", err)
	}
	t.Cleanup(func() { _ = asRole.Close() })
	if _, ddlErr := asRole.ExecContext(t.Context(), `CREATE TABLE probe_ddl (id INTEGER)`); ddlErr == nil {
		t.Fatal("the role created a table, so it is not the role this case is about")
	}

	capability, err := runAssembly(t, roleDSN)
	if err != nil {
		t.Fatalf("a role that may read and write rows could not start on a database whose migrations are "+
			"all applied: %v", err)
	}
	var widgets int64
	if err := capability.DB().Table("widgets").Count(&widgets).Error; err != nil {
		t.Errorf("the role's handle cannot read the application's table: %v", err)
	}
}
