//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestARefusedCommitLeavesTheConnectionOutsideTheTransaction runs the engine-neutral case on this
// engine.
func TestARefusedCommitLeavesTheConnectionOutsideTheTransaction(t *testing.T) {
	dsn := pgtest.Acquire(t)
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn": dsn, "max-open-conns": 1, "max-idle-conns": 1,
	})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	observer, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(observer) })
	dbsuite.RefusedCommit(t, capability.DB(), observer)
}
