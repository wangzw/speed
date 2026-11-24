package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/db"
)

// migrationLockKey is the advisory lock key the whole migration run is held
// under: the FNV-1a 64-bit hash of "speed:db:migrations", written out so every
// replica computes the same number. PostgreSQL scopes advisory locks to one
// database, so processes on other databases are left alone.
const migrationLockKey int64 = 3798569497438843947

// lockPollInterval is how often a waiting replica asks again. The wait polls
// pg_try_advisory_lock because the blocking form has no deadline of its own.
const lockPollInterval = 250 * time.Millisecond

// releaseTimeout bounds the unlock statement, a single round trip against a
// server this connection is already talking to.
const releaseTimeout = 10 * time.Second

// NewMigrationLock returns the cross-process mutex a migration run on this
// engine is held under, giving up after timeout.
func NewMigrationLock(timeout time.Duration) db.MigrationLock {
	return &migrationLock{timeout: timeout}
}

// migrationLock holds the advisory lock on the connection the run pinned. It
// keeps no connection of its own: the lock belongs to the session that took it,
// which must be the one carrying the statements it orders.
type migrationLock struct {
	timeout time.Duration
	held    bool
}

func (l *migrationLock) Acquire(ctx context.Context, conn *sql.Conn) error {
	if l.held {
		return errors.New("postgres: the migration mutex is already held by this process")
	}
	deadline := time.Now().Add(l.timeout)
	for {
		var taken bool
		if err := conn.QueryRowContext(ctx,
			"SELECT pg_try_advisory_lock($1)", migrationLockKey).Scan(&taken); err != nil {
			return fmt.Errorf("postgres: asking for the migration mutex failed: %w", err)
		}
		if taken {
			l.held = true
			return nil
		}
		if !time.Now().Before(deadline) {
			return l.timedOut(ctx, conn)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("postgres: waiting for the migration mutex was cut short: %w", ctx.Err())
		case <-time.After(lockPollInterval):
		}
	}
}

// timedOut reports ErrMigrationLockTimeout and nothing else: what has to be
// looked at is the replica holding the mutex, not a migration file.
func (l *migrationLock) timedOut(ctx context.Context, conn *sql.Conn) error {
	// Asked for rather than taken from the locator, which carries credentials
	// and would reach the startup diagnostics.
	database := "the configured database"
	var name string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err == nil && name != "" {
		database = fmt.Sprintf("database %q", name)
	}
	return fmt.Errorf("%w: another process held the PostgreSQL migration mutex on %s for the whole %s of "+
		"%s.%s. That replica is still applying migrations, or it stopped part-way while holding the mutex; "+
		"find out what it is doing before raising the limit",
		db.ErrMigrationLockTimeout, database, l.timeout, ConfigNamespace, MigrationLockTimeoutKey)
}

func (l *migrationLock) Release(ctx context.Context, conn *sql.Conn) error {
	if !l.held {
		return nil
	}
	l.held = false
	// Detached from the caller's context: the failing path often arrives with
	// it cancelled, and an unlock skipped for that reason would leave the mutex
	// on this session while the other replicas wait out their whole allowance.
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	var released bool
	if err := conn.QueryRowContext(unlockCtx,
		"SELECT pg_advisory_unlock($1)", migrationLockKey).Scan(&released); err != nil {
		return fmt.Errorf("postgres: releasing the migration mutex failed: %w", err)
	}
	if !released {
		return errors.New(
			"postgres: the migration mutex was not held by this connection when it was given up, so " +
				"another replica may have been applying migrations alongside this one")
	}
	return nil
}
