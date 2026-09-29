package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

// TestConcurrentTransactionsThatReadBeforeTheyWriteAllCommit runs the
// engine-neutral case on a database file, over the pool the delivered handle
// opens on it and over one Open builds. SQLite holds one write lock for the
// whole file, and a transaction begun DEFERRED that reads first is refused it
// at once while another holds it; the ones this engine begins wait for it.
func TestConcurrentTransactionsThatReadBeforeTheyWriteAllCommit(t *testing.T) {
	capability := deliver(t, filepath.Join(t.TempDir(), "delivered.db"))
	t.Run("the delivered handle", func(t *testing.T) {
		dbsuite.ReadThenWriteCommits(t, capability.DB())
	})
	t.Run("a handle Open built", func(t *testing.T) {
		opened, err := capability.Open(t.Context(), filepath.Join(t.TempDir(), "opened.db"))
		if err != nil {
			t.Fatalf("opening a second connection: %v", err)
		}
		t.Cleanup(func() { _ = db.Close(opened) })
		dbsuite.ReadThenWriteCommits(t, opened)
	})
}
