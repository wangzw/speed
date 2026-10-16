//go:build integration

package session

import (
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The module runs on PostgreSQL as well in an integration run, over a database of
// the case's own on the server the compose stack brings up: Acquire fails the
// case when that server does not answer rather than skipping it.
func init() {
	dialects = append(dialects, dialectCase{
		name:    "postgres",
		dialect: db.Postgres,
		open:    func(t *testing.T, dsn string) db.Database { return dbtest.Postgres(t, dsn, nil) },
		newDSN:  func(t *testing.T) string { return pgtest.Acquire(t) },
	})
}
