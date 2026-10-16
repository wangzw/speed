//go:build integration

package db

import (
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// PostgreSQL joins the engines on a database of the case's own on the server
// the compose stack brings up. Acquire fails the case when that server does not
// answer rather than skipping it.
func init() {
	engines = append(engines, engine{
		name:     "postgres",
		database: dbpostgres.Module(),
		section:  "postgres",
		locate:   pgtest.Acquire,
	})
}
