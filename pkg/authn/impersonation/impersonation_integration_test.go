//go:build integration

package impersonation

import (
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

func init() {
	dialects = append(dialects, dialect{
		name: "postgres",
		open: func(t *testing.T) *gorm.DB {
			return dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
		},
	})
}
