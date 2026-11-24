package authz

import (
	"context"
	"sync"
)

// TenantLock serialises the writes that decide one tenant's
// tenant-administrator floor. Every path that judges the floor takes it, so the
// host assembles one implementation.
//
// Lock takes the tenant's lock and answers the function that releases it. The
// release is idempotent. An error means the lock could not be taken and the
// caller must not proceed; it is never read as permission.
//
// The lock is not reentrant: routing a binding clear through Manager.DeleteBinding
// would re-enter the lock its removal already holds and deadlock.
//
// The in-process default serialises one process only; replicas that do not share
// a process need a lock the shared database carries. Each database lock a
// removal holds takes one pooled connection and its transaction another, so N
// concurrent removals of one tenant need at least 2N+1 connections.
type TenantLock interface {
	Lock(ctx context.Context, tenant string) (release func(), err error)
}

// InProcessTenantLock is a per-tenant semaphore in one process. It is the
// default TenantLock and the only lock SQLite can offer. An entry is created
// once per tenant and never removed, so a waiting caller never sees a second
// semaphore for the same tenant.
type InProcessTenantLock struct {
	mu       sync.Mutex
	byTenant map[string]chan struct{}
}

// NewInProcessTenantLock builds the default tenant lock.
func NewInProcessTenantLock() *InProcessTenantLock {
	return &InProcessTenantLock{byTenant: make(map[string]chan struct{})}
}

// Lock waits for the tenant's semaphore or for the context to end. An empty
// tenant takes nothing and answers a release that does nothing.
func (l *InProcessTenantLock) Lock(ctx context.Context, tenant string) (func(), error) {
	if tenant == "" {
		return func() {}, nil
	}
	l.mu.Lock()
	semaphore, found := l.byTenant[tenant]
	if !found {
		semaphore = make(chan struct{}, 1)
		l.byTenant[tenant] = semaphore
	}
	l.mu.Unlock()
	select {
	case semaphore <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-semaphore })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
