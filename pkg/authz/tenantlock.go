package authz

import (
	"context"
	"sync"
)

// TenantLock serialises the writes that decide one tenant's
// tenant-administrator floor.
//
// Manager.DeleteBinding counts the domain's tenant-administrator bindings,
// refuses the removal of the last one and removes one, while an explicit
// membership removal asks the same question through
// MembershipGuard.CheckMembershipRemoval. Both read the same count, so both must
// hold the same per-tenant lock: the host assembles one implementation and every
// path that judges the floor takes it. Two locks on one tenant exclude nothing
// of each other, because each path would then wait only for itself.
//
// Lock takes the tenant's lock and answers the function that releases it. The
// release is idempotent. An error means the lock could not be taken and the
// caller must not proceed; it is never read as permission.
//
// The lock is not reentrant. The membership-removal cascade and the
// account-deletion cleanup clear bindings through
// Manager.ClearAccountBindings, which takes no lock; routing either through
// Manager.DeleteBinding would re-enter the lock its removal already holds and
// deadlock.
//
// InProcessTenantLock is the implementation an assembly gets when the host
// supplies none. It serialises this process's removals, needs no configuration
// and costs no database connection. Replicas that do not share a process need
// the host to supply a lock the shared database carries — a session-level
// advisory lock on PostgreSQL — while SQLite stays in the in-process form. Each
// database lock held by a removal takes one pooled connection and the removal's
// transaction takes another, so N concurrent administrator removals of one
// tenant need at least 2N+1 connections; below that, taking the lock waits and
// can time out as unavailable.
type TenantLock interface {
	Lock(ctx context.Context, tenant string) (release func(), err error)
}

// InProcessTenantLock is a per-tenant semaphore in one process. It is the
// default TenantLock and the only lock SQLite can offer, whose delivered handle
// is one process's connection pool.
//
// An entry is created once per tenant and never removed: a tenant set is bounded
// by the roster, and deleting an entry a waiting caller still references would
// let a second caller create a different semaphore for the same tenant.
type InProcessTenantLock struct {
	mu       sync.Mutex
	byTenant map[string]chan struct{}
}

// NewInProcessTenantLock builds the default tenant lock.
func NewInProcessTenantLock() *InProcessTenantLock {
	return &InProcessTenantLock{byTenant: make(map[string]chan struct{})}
}

// Lock waits for the tenant's semaphore or for the context to end. An empty
// tenant names no domain whose floor could be judged, so it takes nothing and
// answers a release that does nothing.
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
