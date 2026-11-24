//go:build integration

package inbox

import (
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

func init() {
	engines = append(engines, engine{
		name:     "postgres",
		database: dbpostgres.Module(),
		section:  "postgres",
		locate:   pgtest.Acquire,
	})
}
