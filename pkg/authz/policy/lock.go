package policy

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

// removalLockNamespace separates this guard's advisory locks from every other
// advisory lock in the same database, including the host's own guard.
const removalLockNamespace = "authz.policy.membership-removal"

// removalLockTimeout bounds the release and the fallback close, not the wait for
// the lock: the caller's context bounds that. A release that cannot run would
// leave the session holding the lock, so it is given time of its own.
const removalLockTimeout = 5 * time.Second

// Lock implements authn.MembershipGuard: it serialises membership removals for
// one tenant so the last-administrator check and the removal's commit cannot be
// interleaved with a second removal that passed the same check.
//
// SQLite has no cross-process lock primitive this module can rely on, and its
// delivered handle is one process's connection pool, so the lock is an
// in-process per-tenant semaphore. PostgreSQL takes a session-level advisory
// lock, which also serialises the replicas that share the database; replicas
// that do not share it are not serialised, which the design states as a
// non-guarantee.
func (p *product) Lock(ctx context.Context, tenant string) (func(), error) {
	if tenant == "" {
		return func() {}, nil
	}
	switch p.dialect {
	case db.SQLite:
		return p.removalLocks.lock(ctx, tenant)
	case db.Postgres:
		return p.advisoryLock(ctx, tenant)
	default:
		return nil, fmt.Errorf("%s: no membership-removal lock for the %q database engine",
			ModuleName, p.dialect)
	}
}

// advisoryLock takes PostgreSQL's session-level advisory lock on a connection of
// its own. The connection is pinned until the release runs because the lock is a
// property of the session: returning it to the pool without unlocking would
// leave the lock held by a connection another request may then use.
func (p *product) advisoryLock(ctx context.Context, tenant string) (func(), error) {
	pool, err := p.handle.DB()
	if err != nil {
		return nil, fmt.Errorf("%s: the database handle has no connection pool: %w", ModuleName, err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: taking a connection for the tenant lock: %w", ModuleName, err)
	}
	key := advisoryKey(tenant)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: taking the advisory lock of tenant %q: %w", ModuleName, tenant, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() { releaseAdvisoryLock(conn, key) })
	}, nil
}

// releaseAdvisoryLock unlocks and returns the connection. A failed unlock is not
// left on a connection that goes back to the pool, where another request could
// inherit the lock: the connection is marked bad so database/sql closes its
// session, and the server drops every session-level lock with it.
func releaseAdvisoryLock(conn *sql.Conn, key int64) {
	ctx, cancel := context.WithTimeout(context.Background(), removalLockTimeout)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

// advisoryKey is the 64-bit key of a tenant's advisory lock. The namespace keeps
// this guard's keys apart from another guard's on the same tenant.
func advisoryKey(tenant string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(removalLockNamespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(tenant))
	// Masking off the sign bit keeps the conversion representable; the lock key
	// needs only to be stable, not to use all 64 bits.
	return int64(h.Sum64() & (1<<63 - 1))
}

// tenantRemovalLocks is the in-process per-tenant semaphore a SQLite assembly
// uses. The entry is created once per tenant and never removed: a tenant set is
// bounded by the roster, and deleting an entry that a waiting caller still holds
// a reference to would let a second caller create a different semaphore for the
// same tenant.
type tenantRemovalLocks struct {
	mu     sync.Mutex
	byName map[string]chan struct{}
}

func newTenantRemovalLocks() *tenantRemovalLocks {
	return &tenantRemovalLocks{byName: make(map[string]chan struct{})}
}

// lock waits for the tenant's semaphore or for the context to end.
func (l *tenantRemovalLocks) lock(ctx context.Context, tenant string) (func(), error) {
	l.mu.Lock()
	semaphore, found := l.byName[tenant]
	if !found {
		semaphore = make(chan struct{}, 1)
		l.byName[tenant] = semaphore
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
