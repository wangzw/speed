package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/db"
)

const tenantLockNamespace = "host.tenancy.membership-removal"

// tenantLockTimeout bounds the release and the fallback close, not the wait for
// the lock: the caller's context bounds that.
const tenantLockTimeout = 5 * time.Second

// Replicas that do not share the database are not serialised; that is the
// assembly's non-guarantee.
type tenantRemovalLock struct {
	database db.Database
	dialect  db.Dialect
	mu       sync.Mutex
	byTenant map[string]chan struct{}
}

func newTenantRemovalLock(database db.Database) *tenantRemovalLock {
	return &tenantRemovalLock{
		database: database,
		dialect:  database.Dialect(),
		byTenant: make(map[string]chan struct{}),
	}
}

func (l *tenantRemovalLock) lock(ctx context.Context, tenant string) (func(), error) {
	if tenant == "" {
		return func() {}, nil
	}
	switch l.dialect {
	case db.SQLite:
		return l.localLock(ctx, tenant)
	case db.Postgres:
		return l.advisoryLock(ctx, tenant)
	default:
		return nil, fmt.Errorf("%s: no membership-removal lock for the %q database engine",
			tenancyModuleName, l.dialect)
	}
}

func (l *tenantRemovalLock) localLock(ctx context.Context, tenant string) (func(), error) {
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

func (l *tenantRemovalLock) advisoryLock(ctx context.Context, tenant string) (func(), error) {
	pool, err := l.database.DB().DB()
	if err != nil {
		return nil, fmt.Errorf("%s: the database handle has no connection pool: %w", tenancyModuleName, err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: taking a connection for the tenant lock: %w", tenancyModuleName, err)
	}
	key := tenantLockKey(tenant)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: taking the advisory lock of tenant %q: %w", tenancyModuleName, tenant, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() { releaseTenantLock(conn, key) })
	}, nil
}

// releaseTenantLock unlocks and returns the connection. A failed unlock marks
// the connection bad, so database/sql closes its session and the server drops
// the lock with it.
func releaseTenantLock(conn *sql.Conn, key int64) {
	ctx, cancel := context.WithTimeout(context.Background(), tenantLockTimeout)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

func tenantLockKey(tenant string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tenantLockNamespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(tenant))
	// Masking off the sign bit keeps the conversion representable; the lock key
	// needs only to be stable, not to use all 64 bits.
	return int64(h.Sum64() & (1<<63 - 1))
}

// Lock implements authn.MembershipGuard and authz.TenantLock: it takes the
// tenant's removal lock so the guard's check and the removal's commit are one
// serialised step.
func (p *tenancyProduct) Lock(ctx context.Context, tenant string) (func(), error) {
	return p.removalLock.lock(ctx, tenant)
}

// MembershipLock implements authn.SharedMembershipLock: it names this product,
// the one lock object both guards and the policy module's binding delete take.
func (p *tenancyProduct) MembershipLock() any { return p }
