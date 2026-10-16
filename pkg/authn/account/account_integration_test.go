//go:build integration

package account

import (
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The PostgreSQL leg of every dialect case, and the seam that keeps it out of
// the unit build: without this file the engine is absent from
// dialectsUnderTest, not skipped within it, so a unit run neither lists it nor
// can reach the server behind it.
//
// The server is the one the repository's compose stack brings up, and a case
// that cannot reach it fails rather than dropping the engine: a run that is
// meant to exercise it must not quietly run one leg of a two-legged case.
func init() {
	dialectsUnderTest = append(dialectsUnderTest, db.Postgres)
	databaseOpeners[db.Postgres] = func(t *testing.T) db.Database {
		return dbtest.Postgres(t, pgtest.Acquire(t), nil)
	}
}
