package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db"
)

// The host's local removal lock serialises one tenant and leaves another
// tenant's removals alone.
func TestTheHostRemovalLockSerialisesOneTenant(t *testing.T) {
	t.Parallel()
	lock := &tenantRemovalLock{dialect: db.SQLite, byTenant: make(map[string]chan struct{})}

	release, err := lock.localLock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking acme's lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, waitErr := lock.localLock(ctx, "acme"); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("a second lock of the same tenant gave %v, want the context's deadline", waitErr)
	}

	releaseAgain, err := lock.localLock(context.Background(), "beta")
	if err != nil {
		t.Fatalf("taking beta's lock beside acme's: %v", err)
	}
	releaseAgain()

	release()

	again, err := lock.localLock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking acme's lock after the release: %v", err)
	}
	again()
}
