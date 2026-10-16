//go:build integration

package impersonation

import (
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The module runs on PostgreSQL as well in an integration run, over a database of
// the case's own on the server the compose stack brings up: Acquire fails the
// case when that server does not answer rather than skipping it.
func init() {
	dialects = append(dialects, dialect{
		name: "postgres",
		open: func(t *testing.T) *gorm.DB {
			return dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
		},
	})
}
