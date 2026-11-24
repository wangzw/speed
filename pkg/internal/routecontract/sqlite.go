package routecontract

import (
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/core"
	dbsqlite "github.com/vislake/speed/pkg/db/sqlite"
)

// SQLite delivers the database capability over a fresh SQLite file, together with the
// configuration subtree that points it at that file.
// Its caller merges the subtree into the case's own document, under the keys it already holds.
func SQLite(t *testing.T) (core.Module, map[string]any) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "routecontract.db")
	return dbsqlite.Module(), map[string]any{
		"db": map[string]any{
			"sqlite": map[string]any{"dsn": dsn},
		},
	}
}
