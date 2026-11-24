package authz

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestTheInProcessTenantLockSerialisesOneTenant checks that a second acquisition
// of one tenant waits until the first release runs.
func TestTheInProcessTenantLockSerialisesOneTenant(t *testing.T) {
	locks := NewInProcessTenantLock()
	release, err := locks.Lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the first lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, waitErr := locks.Lock(ctx, "acme"); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("a second lock of the same tenant gave %v, want the context's deadline", waitErr)
	}

	release()

	again, err := locks.Lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the lock after the release: %v", err)
	}
	again()
}

// TestTheInProcessTenantLockIsPerTenant checks that one tenant's removal does
// not block another's.
func TestTheInProcessTenantLockIsPerTenant(t *testing.T) {
	locks := NewInProcessTenantLock()
	releaseAcme, err := locks.Lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking acme's lock: %v", err)
	}
	defer releaseAcme()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseBeta, err := locks.Lock(ctx, "beta")
	if err != nil {
		t.Fatalf("taking beta's lock beside acme's: %v", err)
	}
	releaseBeta()
}

// TestTheInProcessTenantLockReleaseIsIdempotent checks that calling a release
// twice cannot free a second acquisition.
func TestTheInProcessTenantLockReleaseIsIdempotent(t *testing.T) {
	locks := NewInProcessTenantLock()
	release, err := locks.Lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	release()
	release()

	again, err := locks.Lock(context.Background(), "acme")
	if err != nil {
		t.Fatalf("taking the lock after a doubled release: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, waitErr := locks.Lock(ctx, "acme"); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("a second lock after the doubled release gave %v, want the context's deadline", waitErr)
	}
	again()
}

// TestTheInProcessTenantLockTakesNothingForAnEmptyTenant checks that an empty
// tenant is never serialised.
func TestTheInProcessTenantLockTakesNothingForAnEmptyTenant(t *testing.T) {
	locks := NewInProcessTenantLock()
	first, err := locks.Lock(context.Background(), "")
	if err != nil {
		t.Fatalf("taking the empty tenant's lock: %v", err)
	}
	defer first()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := locks.Lock(ctx, "")
	if err != nil {
		t.Fatalf("taking the empty tenant's lock again: %v", err)
	}
	second()
}
