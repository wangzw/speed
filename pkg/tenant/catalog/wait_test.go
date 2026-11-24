package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// The cases in this file are about how long the roster's own statements wait
// for a connection and for a lock. pkg/db holds an in-memory SQLite database to
// one connection, and a caller inside its own transaction on the delivered
// handle holds that connection until its own call returns: a statement of the
// roster's that waits for it is waiting for the caller. An in-process bus runs
// a subscriber on its publisher's goroutine, inside whatever transaction the
// publisher holds, and the subscriber enters the event's tenant. A lock another
// connection holds is waited for as the engine waits for it, which
// connectionWait does not reach.

// connectionTimer is how long a case waits for a call bounded by
// connectionWait before it reports the call as still waiting.
func connectionTimer() time.Duration {
	return connectionWait + coretest.Scale(3*time.Second)
}

// heldToOneConnection is enteringRoster over a pool of one connection, the
// shape pkg/db delivers an in-memory database in.
func heldToOneConnection(t *testing.T) *roster {
	t.Helper()
	r := enteringRoster(t)
	pool, err := r.handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool: %v", err)
	}
	pool.SetMaxOpenConns(1)
	return r
}

// insideTheCallersTransaction runs call inside a transaction the caller holds
// on the roster's delivered handle, having written in it first, and returns
// how long the call took and what it gave; a call still waiting after
// connectionTimer fails the case.
func insideTheCallersTransaction(t *testing.T, r *roster, call func() error) (time.Duration, error) {
	t.Helper()
	type result struct {
		err  error
		took time.Duration
	}
	done := make(chan result, 1)
	go func() {
		var got result
		err := r.handle.Transaction(func(tx *gorm.DB) error {
			if err := tx.WithContext(crossContext()).
				Exec("UPDATE orders SET note = 'held' WHERE id = 'a'").Error; err != nil {
				return err
			}
			start := time.Now()
			got.err = call()
			got.took = time.Since(start)
			return nil
		})
		if err != nil {
			got.err = err
		}
		done <- got
	}()
	limit := connectionTimer()
	select {
	case got := <-done:
		return got.took, got.err
	case <-time.After(limit):
		t.Fatalf("the call inside the caller's transaction on a database held to one connection was "+
			"still waiting after %v", limit)
		return 0, nil
	}
}

// requireNoAnswer asserts that a call gave a failure that is none of the
// answers a caller acts on: neither refusal, and not the table's absence, which
// is there all along.
func requireNoAnswer(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDisabled) ||
		errors.Is(err, tenant.ErrRosterMissing) {
		t.Fatalf("%s gave %v, want a failure that is neither refusal nor ErrRosterMissing", what, err)
	}
}

// requireNoConnection asserts that a call gave up waiting for a connection,
// and said so, with no answer about the tenant.
func requireNoConnection(t *testing.T, what string, err error) {
	t.Helper()
	requireNoAnswer(t, what, err)
	if !errors.Is(err, tenant.ErrNoConnection) {
		t.Fatalf("%s gave %v, want ErrNoConnection", what, err)
	}
}

// An entry the snapshot would refuse reads the table, and inside the caller's
// transaction on a database held to one connection that read cannot get one: it
// gives up once connectionWait has passed, with no answer rather than either
// refusal, and hands back a context that is no tenant's. An entry the snapshot
// admits reads nothing, and answers at once.
func TestAnEntryInsideTheCallersTransactionOnOneConnectionEndsWithoutAnAnswer(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		enter func(r *roster, ctx context.Context) (context.Context, error)
	}{
		{"Enter a tenant no row carries", func(r *roster, ctx context.Context) (context.Context, error) {
			return r.Enter(ctx, "t_ghost")
		}},
		{"Enter a disabled tenant", func(r *roster, ctx context.Context) (context.Context, error) {
			return r.Enter(ctx, "t_paused")
		}},
		{"EnterRegistered a tenant no row carries", func(r *roster, ctx context.Context) (context.Context, error) {
			return r.EnterRegistered(ctx, "t_ghost")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := heldToOneConnection(t)
			var entered context.Context
			took, err := insideTheCallersTransaction(t, r, func() error {
				var err error
				entered, err = c.enter(r, crossContext())
				return err
			})
			requireNoConnection(t, c.name, err)
			if took < connectionWait {
				t.Fatalf("the entry gave up after %v, before the %v a connection is waited for", took, connectionWait)
			}
			if id, ok := tenant.FromContext(entered); ok || tenantctx.CrossTenant(entered) {
				t.Fatalf("an entry with no answer handed back tenant (%q, %v), crossing %v; want neither",
					id, ok, tenantctx.CrossTenant(entered))
			}
		})
	}
	t.Run("Enter a tenant the snapshot serves", func(t *testing.T) {
		t.Parallel()
		r := heldToOneConnection(t)
		took, err := insideTheCallersTransaction(t, r, func() error {
			_, err := r.Enter(context.Background(), "t_acme")
			return err
		})
		if err != nil || took >= connectionWait {
			t.Fatalf("entering a tenant the snapshot serves gave %v after %v, want the tenant at once", err, took)
		}
	})
}

// A write of the roster runs on a connection of its own, and inside the
// caller's transaction on a database held to one connection it cannot get one:
// it gives up once connectionWait has passed, and nothing is written.
func TestARosterWriteInsideTheCallersTransactionOnOneConnectionEnds(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]func(r *roster) error{
		"Put": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"Create": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func(r *roster) error {
			return r.SetStatus(context.Background(), "t_paused", tenant.StatusActive)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := heldToOneConnection(t)
			_, err := insideTheCallersTransaction(t, r, func() error { return write(r) })
			requireNoConnection(t, name, err)
			for id, want := range map[tenant.ID]bool{"t_new": false, "t_paused": false} {
				held, err := lookUp(r.handle, "id", string(id))
				if err != nil {
					t.Fatalf("reading the table back: %v", err)
				}
				if served := len(held) > 0 && held[0].Status == int(tenant.StatusActive); served != want {
					t.Fatalf("after a write that gave up, table %s serves %s: %v", tableName, id, served)
				}
			}
		})
	}
}

// A reread of the whole roster — the refresher's, a write's once it has
// committed, an entry's bringing the snapshot up to the table — inside the
// caller's transaction on a database held to one connection gives up once
// connectionWait has passed, with ErrNoConnection, and the snapshot it would
// have replaced stays in force.
//
// Each reread holds the one slot reads of the table run in while it waits, so
// a reread that waited for the caller for good would hold every other reread,
// and every entry catching the snapshot up, with it.
func TestAReadOfTheWholeRosterInsideTheCallersTransactionOnOneConnectionEnds(t *testing.T) {
	r := heldToOneConnection(t)
	before := r.snap.Load()
	took, err := insideTheCallersTransaction(t, r, func() error { return r.read(context.Background()) })
	requireNoConnection(t, "the reread of the whole roster", err)
	if took < connectionWait {
		t.Fatalf("the reread gave up after %v, before the %v a connection is waited for", took, connectionWait)
	}
	if r.snap.Load() != before {
		t.Fatal("a reread that read nothing replaced the snapshot")
	}
}

// rosterOverALockedFile is enteringRoster's roster over a database file, the
// locator's parameters given, with another connection holding the whole
// database in a transaction until the case ends.
func rosterOverALockedFile(t *testing.T, parameters string) *roster {
	t.Helper()
	file := filepath.Join(t.TempDir(), "locked.db")
	r := rosterOn(t, openHandleAt(t, file+parameters), "sqlite",
		insert("t_acme", "acme"), "INSERT INTO tenants (id, name, status) VALUES ('t_paused', 'paused', 0)")
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}
	other, err := dbtest.SQLite(t, file, nil).DB().DB()
	if err != nil {
		t.Fatalf("reaching the other pool: %v", err)
	}
	holder, err := other.Conn(context.Background())
	if err != nil {
		t.Fatalf("taking a connection of the other pool: %v", err)
	}
	if _, err := holder.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("holding the database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = holder.ExecContext(context.Background(), "ROLLBACK")
		_ = holder.Close()
	})
	return r
}

// lockedCalls are the calls of the roster's that read or write its table, each
// with how many of its statements wait for a lock held on the whole database:
// the statement that fails, and each read the call then makes to say what the
// failure was — the conflict a write may have met, and the table's absence.
func lockedCalls() map[string]struct {
	call     func(r *roster) error
	waitsFor int
} {
	return map[string]struct {
		call     func(r *roster) error
		waitsFor int
	}{
		"Enter a tenant no row carries": {func(r *roster) error {
			_, err := r.Enter(context.Background(), "t_ghost")
			return err
		}, 2},
		"Enter a disabled tenant": {func(r *roster) error {
			_, err := r.Enter(context.Background(), "t_paused")
			return err
		}, 2},
		"EnterRegistered a tenant no row carries": {func(r *roster) error {
			_, err := r.EnterRegistered(context.Background(), "t_ghost")
			return err
		}, 2},
		"Put": {func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		}, 3},
		"Create": {func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		}, 4},
		"SetStatus": {func(r *roster) error {
			return r.SetStatus(context.Background(), "t_paused", tenant.StatusActive)
		}, 2},
		"a reread of the whole roster": {func(r *roster) error { return r.read(context.Background()) }, 2},
	}
}

// lockBusyTimeout is the busy timeout the locator in the case below sets: short
// of the driver's default and of connectionWait, so that a lock wait held to
// either is told apart from one held to the locator's.
const lockBusyTimeout = time.Second

// A statement of the roster's that meets a lock another connection holds on a
// SQLite file waits for it for the connection's busy timeout, the one the
// locator sets, whatever connectionWait is; so does each read the call makes to
// say what the failure was. The call then ends with no answer: neither refusal,
// and not the table's absence, since the table is there. connectionWait bounds
// the wait for a connection only.
func TestAStatementOfTheRostersWaitsForALockForTheBusyTimeout(t *testing.T) {
	t.Parallel()
	for name, c := range lockedCalls() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := rosterOverALockedFile(t, "?_pragma=busy_timeout(1000)")
			// A wrong implementation applies the connection wait where the
			// engine's busy timeout bounds the lock, so widening it makes that
			// implementation need an hour while the right one still gives up
			// after the locator's second. The bound is the busy timeout plus two
			// seconds through GATE_TIME_SCALE alone; the hour is the wrong time
			// stated to DeclaredAgainst.
			r.connectWithin = time.Hour
			most := time.Duration(c.waitsFor) * lockBusyTimeout
			done := make(chan error, 1)
			started := time.Now()
			go func() { done <- c.call(r) }()
			bound := connectionTimer()
			select {
			case err := <-done:
				requireNoAnswer(t, name, err)
				if errors.Is(err, tenant.ErrNoConnection) {
					t.Fatalf("%s gave %v, want the lock's failure rather than a connection's", name, err)
				}
				if !strings.Contains(err.Error(), "SQLITE_BUSY") {
					t.Fatalf("%s gave %v, want SQLite's refusal of the lock", name, err)
				}
				took := time.Since(started)
				if took < lockBusyTimeout {
					t.Fatalf("%s gave up after %v, before the busy timeout of %v", name, took, lockBusyTimeout)
				}
				if limit := coretest.DeclaredAgainst(t, most, 2*time.Second, time.Hour); took > limit {
					t.Fatalf("%s gave up after %v, past a busy timeout of %v for each of its %d statements",
						name, took, lockBusyTimeout, c.waitsFor)
				}
			case <-time.After(most + bound):
				t.Fatalf("%s was still waiting for the lock after %v", name, most+bound)
			}
		})
	}
}

// Inside a caller's transaction that has written to the roster's SQLite file, a
// write of the roster's waits for the caller's write lock for the connection's
// busy timeout, the one the locator sets, and then fails with the engine's
// refusal and no answer, and nothing is written: the database module begins
// the write's transaction IMMEDIATE, so it asks for the write lock at its BEGIN,
// and the caller holds that lock until its own call returns. Only the BEGIN
// waits; the reads the write then makes to say what the failure was do not,
// since the caller holds no lock that stops a reader. An entry only reads, and
// answers from the table.
func TestARosterWriteInsideTheCallersWriteTransactionOnAFileWaitsForTheBusyTimeout(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]func(r *roster) error{
		"Put": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"Create": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func(r *roster) error {
			return r.SetStatus(context.Background(), "t_paused", tenant.StatusActive)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := rosterOn(t, openHandleAt(t, filepath.Join(t.TempDir(), "roster.db")+"?_pragma=busy_timeout(1000)"),
				"sqlite", insert("t_acme", "acme"), "INSERT INTO tenants (id, name, status) VALUES ('t_paused', 'paused', 0)")
			// A wrong implementation applies the connection wait where the
			// engine's busy timeout bounds the lock, so widening it makes that
			// implementation need an hour while the right one still gives up
			// after the locator's second. The bound is the busy timeout plus two
			// seconds through GATE_TIME_SCALE alone; the hour is the wrong time
			// stated to DeclaredAgainst.
			r.connectWithin = time.Hour
			if err := r.read(context.Background()); err != nil {
				t.Fatalf("reading the roster: %v", err)
			}
			type written struct {
				took    time.Duration
				err     error
				entered error
			}
			done := make(chan written, 1)
			go func() {
				var res written
				res.err = r.handle.Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("UPDATE tenants SET name = 'acme2' WHERE id = 't_acme'").Error; err != nil {
						return err
					}
					started := time.Now()
					werr := write(r)
					res.took = time.Since(started)
					_, res.entered = r.Enter(context.Background(), "t_paused")
					return werr
				})
				done <- res
			}()
			limit := coretest.DeclaredAgainst(t, lockBusyTimeout, 2*time.Second, time.Hour)
			var res written
			select {
			case res = <-done:
			case <-time.After(limit):
				t.Fatalf("%s had not given up on the lock within %v: it is waiting on the connection's %v "+
					"bound rather than on the busy timeout of %v", name, limit, r.connectWithin, lockBusyTimeout)
			}
			requireNoAnswer(t, name, res.err)
			if !strings.Contains(res.err.Error(), "SQLITE_BUSY") {
				t.Fatalf("%s gave %v, want SQLite's refusal of the lock", name, res.err)
			}
			if res.took < lockBusyTimeout {
				t.Fatalf("%s gave up after %v, before the busy timeout of %v", name, res.took, lockBusyTimeout)
			}
			if res.took > limit {
				t.Fatalf("%s gave up after %v, past the one busy timeout of %v its BEGIN waits for",
					name, res.took, lockBusyTimeout)
			}
			entered := res.entered
			if !errors.Is(entered, tenant.ErrTenantDisabled) {
				t.Fatalf("an entry inside the same transaction gave %v, want ErrTenantDisabled from the table", entered)
			}
			for id, want := range map[tenant.ID]bool{"t_new": false, "t_paused": false} {
				held, err := lookUp(r.handle, "id", string(id))
				if err != nil {
					t.Fatalf("reading the table back: %v", err)
				}
				if served := len(held) > 0 && held[0].Status == int(tenant.StatusActive); served != want {
					t.Fatalf("after a write that failed, table %s serves %s: %v", tableName, id, served)
				}
			}
		})
	}
}

// A connection the pool cannot open is no answer either, and not the table's
// absence: nothing was read.
func TestAConnectionThatCannotBeOpenedIsNotAMissingTable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "roster")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("making the database's directory: %v", err)
	}
	r := rosterOn(t, openHandleAt(t, filepath.Join(dir, "roster.db")), "sqlite", insert("t_acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}
	pool, err := r.handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool: %v", err)
	}
	// With no connection kept idle, the next statement opens one, and the
	// directory the file was in is a file now.
	pool.SetMaxIdleConns(0)
	if err = os.RemoveAll(dir); err != nil {
		t.Fatalf("removing the database's directory: %v", err)
	}
	if err = os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatalf("putting a file where the directory was: %v", err)
	}
	_, err = r.Enter(context.Background(), "t_ghost")
	requireNoAnswer(t, "entering with no connection to be had", err)
	if errors.Is(err, tenant.ErrNoConnection) {
		t.Fatalf("entering with no connection to be had gave %v, want the failure to open one", err)
	}
}

// A connection another caller holds for the time a statement takes comes free
// inside the bound, and the entry waits for it and answers from the table.
func TestAnEntryWaitsForAConnectionThatComesFree(t *testing.T) {
	r := heldToOneConnection(t)
	pool, err := r.handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool: %v", err)
	}
	conn, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatalf("taking the one connection: %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = conn.Close()
	}()
	if _, err := r.Enter(context.Background(), "t_ghost"); !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("entering a tenant no row carries while the connection was briefly taken gave %v, "+
			"want ErrInvalidTenant", err)
	}
}

// The caller's context ends the table read an entry makes, whether the read is
// waiting for a connection or running: a caller that set a deadline is answered
// by it rather than by connectionWait or by the read.
func TestAnEntrysTableReadEndsWithTheCallersContext(t *testing.T) {
	const deadline = 300 * time.Millisecond
	requireEndedByTheCaller := func(t *testing.T, err error, took time.Duration) {
		t.Helper()
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, tenant.ErrNoConnection) {
			t.Fatalf("the entry gave %v, want the caller's deadline", err)
		}
		requireNoAnswer(t, "the entry", err)
		if took >= connectionWait/2 {
			t.Fatalf("the entry ended after %v, want it ended by the caller's %v", took, deadline)
		}
	}

	t.Run("waiting for a connection", func(t *testing.T) {
		t.Parallel()
		r := heldToOneConnection(t)
		took, err := insideTheCallersTransaction(t, r, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			_, err := r.Enter(ctx, "t_ghost")
			return err
		})
		requireEndedByTheCaller(t, err, took)
	})

	t.Run("reading", func(t *testing.T) {
		t.Parallel()
		r := enteringRoster(t)
		// The read of the row runs until its statement's context ends, and
		// then fails with it, as a driver that honours a context does; a
		// statement whose context is not the caller's runs long past the
		// caller's deadline and reads the row.
		var held atomic.Bool
		if err := r.handle.Callback().Query().Before("gorm:query").Register("test:slow-row-read",
			func(tx *gorm.DB) {
				if _, conditioned := tx.Statement.Clauses["WHERE"]; !conditioned || tx.Statement.Table != tableName {
					return
				}
				held.Store(true)
				select {
				case <-tx.Statement.Context.Done():
					_ = tx.AddError(tx.Statement.Context.Err())
				case <-time.After(connectionWait):
				}
			}); err != nil {
			t.Fatalf("registering the callback: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		start := time.Now()
		_, err := r.Enter(ctx, "t_ghost")
		took := time.Since(start)
		if !held.Load() {
			t.Fatal("the entry read no row of the table")
		}
		requireEndedByTheCaller(t, err, took)
	})
}
