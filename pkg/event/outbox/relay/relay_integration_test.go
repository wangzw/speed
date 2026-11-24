//go:build integration

package relay

import (
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// postgresEngine is the leg that reads and writes on a server of the compose
// stack's, which the untagged build must not reach: the fixture hands out a
// locator, and opening one needs the server up.
var postgresEngine = engine{
	name:   "postgres",
	locate: pgtest.Acquire,
	open:   func(t *testing.T, dsn string) db.Database { return dbtest.Postgres(t, dsn, nil) },
}

func init() { engines = append(engines, postgresEngine) }
