//go:build integration

package account

import (
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// A run that cannot reach the server fails rather than dropping this leg, so a two-legged case never quietly runs one.
func init() {
	dialectsUnderTest = append(dialectsUnderTest, db.Postgres)
	databaseOpeners[db.Postgres] = func(t *testing.T) db.Database {
		return dbtest.Postgres(t, pgtest.Acquire(t), nil)
	}
}
