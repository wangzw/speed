package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
)

const rolePassword = "pgtest-role"

const roleInfix = "_r"

var roleCounter atomic.Int64

// DMLRole creates a login role that may read and write the rows of every table, and use every
// sequence, that the public schema of dsn's database holds at the moment of the call, and that may
// create nothing.
func DMLRole(t *testing.T, dsn string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("reading the locator %q: %v", dsn, err)
	}
	database := parsed.Path
	if len(database) > 0 && database[0] == '/' {
		database = database[1:]
	}
	name := fmt.Sprintf("%s%d%s%d", namePrefix, os.Getpid(), roleInfix, roleCounter.Add(1))
	role := pgx.Identifier{name}.Sanitize()

	owner := openHandle(t, dsn)
	for _, statement := range []string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, role, rolePassword),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %s TO %s`, pgx.Identifier{database}.Sanitize(), role),
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, role),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, role),
		fmt.Sprintf(`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s`, role),
	} {
		if err := execute(owner, statement); err != nil {
			t.Fatalf("setting up the role %s: %s: %v", name, statement, err)
		}
	}
	t.Cleanup(func() {
		for _, statement := range []string{`DROP OWNED BY ` + role, `DROP ROLE ` + role} {
			if err := execute(owner, statement); err != nil {
				t.Errorf("removing the role %s: %s: %v", name, statement, err)
			}
		}
	})

	parsed.User = url.UserPassword(name, rolePassword)
	asRole := parsed.String()
	probe := openHandle(t, asRole)
	if err := execute(probe, `CREATE TABLE pgtest_ddl_probe (id INTEGER)`); err == nil {
		t.Fatalf("the role %s created a table, so it is not a role that may only read and write rows", name)
	}
	return asRole
}

func openHandle(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	handle, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening a handle: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

func execute(handle *sql.DB, statement string) error {
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()
	_, err := handle.ExecContext(ctx, statement)
	return err
}
