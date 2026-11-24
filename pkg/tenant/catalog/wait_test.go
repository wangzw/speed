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

var connectionTimer = connectionWait + coretest.Scale(3*time.Second)

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
	select {
	case got := <-done:
		return got.took, got.err
	case <-time.After(connectionTimer):
		t.Fatalf("the call inside the caller's transaction on a database held to one connection was "+
			"still waiting after %v", connectionTimer)
		return 0, nil
	}
}

func requireNoAnswer(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDisabled) ||
		errors.Is(err, tenant.ErrRosterMissing) {
		t.Fatalf("%s gave %v, want a failure that is neither refusal nor ErrRosterMissing", what, err)
	}
}

func requireNoConnection(t *testing.T, what string, err error) {
	t.Helper()
	requireNoAnswer(t, what, err)
	if !errors.Is(err, tenant.ErrNoConnection) {
		t.Fatalf("%s gave %v, want ErrNoConnection", what, err)
	}
}

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

const lockBusyTimeout = time.Second

func TestAStatementOfTheRostersWaitsForALockForTheBusyTimeout(t *testing.T) {
	t.Parallel()
	for name, c := range lockedCalls() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := rosterOverALockedFile(t, "?_pragma=busy_timeout(1000)")
			r.connectWithin = time.Hour
			most := time.Duration(c.waitsFor) * lockBusyTimeout
			done := make(chan error, 1)
			started := time.Now()
			go func() { done <- c.call(r) }()
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
			case <-time.After(most + connectionTimer):
				t.Fatalf("%s was still waiting for the lock after %v", name, most+connectionTimer)
			}
		})
	}
}

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
