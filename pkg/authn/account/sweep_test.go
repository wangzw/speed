package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Start refuses a database that was not migrated by a command that took the module in statically.
func TestStartRefusesAMissingTable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		if err := h.handle.Exec("DROP TABLE " + tableThrottles).Error; err != nil {
			t.Fatalf("dropping a table: %v", err)
		}
		if err := h.p.start(t.Context()); !errors.Is(err, authn.ErrNotMigrated) {
			t.Errorf("Start over a missing table gave %v, want ErrNotMigrated", err)
		}
	})
}

// The probe covers every table, the pending contacts among them: a database migrated before one existed refuses startup and names it.
func TestStartRefusesAMissingPendingContactTable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		if err := h.handle.Exec("DROP TABLE " + tablePending).Error; err != nil {
			t.Fatalf("dropping a table: %v", err)
		}
		err := h.p.start(t.Context())
		if !errors.Is(err, authn.ErrNotMigrated) {
			t.Fatalf("Start over a missing pending-contact table gave %v, want ErrNotMigrated", err)
		}
		if !strings.Contains(err.Error(), tablePending) {
			t.Errorf("the refusal %q does not name %s", err, tablePending)
		}
	})
}

// Start brings the sweeps up and Stop waits for them within the module's own bound.
func TestStartAndStopTheSweepsWithinTheBound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		if err := h.p.start(t.Context()); err != nil {
			t.Fatalf("Starting: %v", err)
		}
		started := time.Now()
		h.p.stop(context.Background())
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Errorf("Stop took %v, want it within five seconds", elapsed)
		}
	})
}

// A flood of random identifiers is reclaimed once the window passes without a failure and the waits are over.
func TestTheThrottleSweepReclaimsAFlood(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const flood = 200
		for i := range flood {
			if _, err := h.p.Fail(ctx, authn.ScopeIdentifier, fmt.Sprintf("spray-%d", i)); err != nil {
				t.Fatalf("flooding: %v", err)
			}
		}
		if n := h.count(tableThrottles, ""); n != flood {
			t.Fatalf("the flood left %d rows, want %d", n, flood)
		}
		h.clock.advance(defaults.ThrottleDecay + time.Minute)
		if _, err := h.p.sweepThrottles(ctx); err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if n := h.count(tableThrottles, ""); n != 0 {
			t.Errorf("the sweep left %d row(s), want the table reclaimed to its size before the flood", n)
		}
	})
}

// A pass that deletes a full batch continues, so one pass reclaims more than a batch.
func TestTheThrottleSweepReclaimsMoreThanOneBatchInAPass(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		flood := sweepBatch + 1
		for i := range flood {
			if _, err := h.p.Fail(ctx, authn.ScopeIdentifier, fmt.Sprintf("overflow-%d", i)); err != nil {
				t.Fatalf("flooding: %v", err)
			}
		}
		if n := h.count(tableThrottles, ""); n != int64(flood) {
			t.Fatalf("the flood left %d rows, want %d", n, flood)
		}
		h.clock.advance(defaults.ThrottleDecay + time.Minute)
		if _, err := h.p.sweepThrottles(ctx); err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if n := h.count(tableThrottles, ""); n != 0 {
			t.Errorf("one pass left %d row(s), want a table larger than one batch reclaimed", n)
		}
	})
}

// A key failed within the window is not swept: the count on it still stands.
func TestTheThrottleSweepKeepsAKeyWithinItsWindow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const key = "recent"
		for range 3 {
			if _, err := h.p.Fail(ctx, authn.ScopeAccount, key); err != nil {
				t.Fatalf("failing: %v", err)
			}
		}
		h.clock.advance(time.Minute)
		if _, err := h.p.sweepThrottles(ctx); err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if n, ok := h.failuresOf(authn.ScopeAccount, key); !ok || n != 3 {
			t.Errorf("the sweep left %d failures (present %v), want the key within its window kept", n, ok)
		}
	})
}

// A service secret is swept once it expired or was revoked before retention began, and a live one is left alone.
func TestTheSecretSweepDeletesOldRows(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		service := h.service("robot")
		revokedID, _, err := h.p.CreateServiceSecret(ctx, service, "revoked", nil)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		if err := h.p.RevokeServiceSecret(ctx, service, revokedID); err != nil {
			t.Fatalf("revoking: %v", err)
		}
		if _, _, err := h.p.CreateServiceSecret(ctx, service, "live", nil); err != nil {
			t.Fatalf("making a live secret: %v", err)
		}
		h.clock.advance(defaults.SecretRetention + time.Hour)
		if _, err := h.p.sweepSecrets(ctx); err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if n := h.count(tableServiceKeys, "id = ?", revokedID); n != 0 {
			t.Errorf("the revoked secret is %d rows, want it swept past retention", n)
		}
		if n := h.count(tableServiceKeys, ""); n != 1 {
			t.Errorf("%d service secrets left, want the live one kept", n)
		}
	})
}
