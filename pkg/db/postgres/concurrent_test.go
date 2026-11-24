//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestConcurrentTransactionsThatReadBeforeTheyWriteAllCommit runs the engine-neutral case on the
// handle this engine delivers, whose pool gives each transaction a connection of its own.
func TestConcurrentTransactionsThatReadBeforeTheyWriteAllCommit(t *testing.T) {
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{"dsn": pgtest.Acquire(t)})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	dbsuite.ReadThenWriteCommits(t, capability.DB())
}
