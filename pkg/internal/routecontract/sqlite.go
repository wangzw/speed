package routecontract

import (
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/core"
	dbsqlite "github.com/vislake/speed/pkg/db/sqlite"
)

// SQLite delivers the database capability over a fresh SQLite file, together
// with the configuration subtree that points it at that file. Its caller
// merges the subtree into the case's own document, under the keys it already
// holds.
//
// A module that takes a database up in New and asks it for a handle — the
// session and token store, the notification and object stores do — needs a real
// one: a stand-in that only satisfies the interface panics on the first call.
// A database with no schema yet is enough, because the check drives to Init and
// nothing has read a table by then.
func SQLite(t *testing.T) (core.Module, map[string]any) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "routecontract.db")
	return dbsqlite.Module(), map[string]any{
		"db": map[string]any{
			"sqlite": map[string]any{"dsn": dsn},
		},
	}
}
