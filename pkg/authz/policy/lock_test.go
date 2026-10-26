package policy

import (
	"context"
	"errors"
	"testing"
	"time"
)

// One tenant's removals are serialised: a second acquisition waits until the
// first release runs.
func TestTheRemovalLockSerialisesOneTenant(t *testing.T) {
	locks := newTenantRemovalLocks()
	release, err := locks.lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the first lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, waitErr := locks.lock(ctx, "acme"); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("a second lock of the same tenant gave %v, want the context's deadline", waitErr)
	}

	release()

	again, err := locks.lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the lock after the release: %v", err)
	}
	again()
}

// The lock is per tenant: one tenant's removal does not block another's.
func TestTheRemovalLockIsPerTenant(t *testing.T) {
	locks := newTenantRemovalLocks()
	releaseAcme, err := locks.lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking acme's lock: %v", err)
	}
	defer releaseAcme()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseBeta, err := locks.lock(ctx, "beta")
	if err != nil {
		t.Fatalf("taking beta's lock beside acme's: %v", err)
	}
	releaseBeta()
}

// A release is idempotent: calling it twice cannot free a second acquisition.
func TestTheRemovalLockReleaseIsIdempotent(t *testing.T) {
	locks := newTenantRemovalLocks()
	release, err := locks.lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	release()
	release()

	again, err := locks.lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the lock after a doubled release: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, waitErr := locks.lock(ctx, "acme"); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("a second lock after the doubled release gave %v, want the context's deadline", waitErr)
	}
	again()
}
