//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestANonPositiveMutexTimeoutFailsTheStartupOnThisEngine pins the refusal of a timeout that would
// make the mutex a single attempt, on the module this subpackage registers.
func TestANonPositiveMutexTimeoutFailsTheStartupOnThisEngine(t *testing.T) {
	// A startup that went ahead runs until its context ends, so the deadline
	// here is what ends the run the refusal should have stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	returned, err := startupAgainst(t, ctx, map[string]any{
		"dsn":                      pgtest.Acquire(t),
		db.MigrationLockTimeoutKey: "0s",
	}, func() {})
	if !returned {
		t.Fatal("the startup did not end")
	}
	if err == nil {
		t.Fatal("a startup with a migration-lock-timeout of 0s went ahead")
	}
	if want := postgres.ConfigNamespace + "." + db.MigrationLockTimeoutKey; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name %s: %v", want, err)
	}
}
