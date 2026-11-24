//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

func carriesPostgresError(err error) bool {
	var refusal *pgconn.PgError
	return errors.As(err, &refusal)
}

// TestConstraintRefusalsOnTheDeliveredHandle runs the engine-neutral cases on the handle this
// engine's module delivers.
func TestConstraintRefusalsOnTheDeliveredHandle(t *testing.T) {
	capability, err := runAssembly(t, pgtest.Acquire(t))
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	dbsuite.ConstraintRefusals(t, capability.DB(), carriesPostgresError)
}

// TestConstraintRefusalsOnAnOpenedHandle runs them on a connection built with Open, which is
// assembled the way the delivered one is.
func TestConstraintRefusalsOnAnOpenedHandle(t *testing.T) {
	capability, err := runAssembly(t, pgtest.Acquire(t))
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	opened, err := capability.Open(t.Context(), pgtest.Acquire(t))
	if err != nil {
		t.Fatalf("opening a second connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(opened) })
	dbsuite.ConstraintRefusals(t, opened, carriesPostgresError)
}
