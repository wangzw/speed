//go:build integration

package preferences

import (
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The module is exercised on PostgreSQL as well in an integration run, over a
// database of the case's own on the server the compose stack brings up.
func init() {
	engines = append(engines, engine{
		name:     "postgres",
		database: dbpostgres.Module(),
		section:  "postgres",
		locate:   pgtest.Acquire,
	})
}
