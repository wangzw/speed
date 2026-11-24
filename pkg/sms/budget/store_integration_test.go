//go:build integration

package budget

import (
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

func init() {
	dialects = append(dialects, dialect{
		name: string(db.Postgres),
		open: func(t *testing.T, items dbtest.Items) db.Database {
			return dbtest.Postgres(t, pgtest.Acquire(t), items)
		},
	})
}
