//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestARefusedCommitLeavesTheConnectionOutsideTheTransaction runs the
// engine-neutral case on this engine. PostgreSQL ends a transaction whose
// COMMIT it refuses, and its driver closes a connection a failed COMMIT leaves
// in any other state, so nothing of this module stands behind the contract
// here; the case is what holds the engine to it. The pool is held to one
// connection, so every statement after the refusal lands on the connection
// the refused transaction ran on, and a second pool on the database is what
// sees whether a write made there was committed.
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
