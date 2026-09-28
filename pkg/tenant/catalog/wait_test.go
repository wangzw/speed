package catalog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// The cases in this file are about how long the roster's own statements wait
// for a connection. pkg/db holds an in-memory SQLite database to one
// connection, and a caller inside its own transaction on the delivered handle
// holds that connection until its own call returns: a statement of the
// roster's that waits for it is waiting for the caller. An in-process bus runs
// a subscriber on its publisher's goroutine, inside whatever transaction the
// publisher holds, and the subscriber enters the event's tenant.

// connectionTimer is how long a case waits for a call bounded by
// connectionWait before it reports the call as still waiting.
const connectionTimer = connectionWait + 3*time.Second

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
	select {
	case got := <-done:
		return got.took, got.err
	case <-time.After(connectionTimer):
		t.Fatalf("the call inside the caller's transaction on a database held to one connection was "+
			"still waiting after %v", connectionTimer)
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
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"Create": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
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
