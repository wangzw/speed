package settings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
)

// connectionTimer is how long a case waits for a call that should give up on
// a connection within connectionWait, before reporting it as still waiting.
const connectionTimer = connectionWait + 3*time.Second

// storeOnOneConnection builds a loaded store over an in-memory database held to
// a single connection, the shape the database module delivers a
// connection-scoped SQLite locator in, and seeds demo.limit's global row with 3.
func storeOnOneConnection(t *testing.T) (*settingsStore, *gorm.DB, *recordingBus) {
	t.Helper()
	handle, err := gorm.Open(sqlitedriver.Open(":memory:"), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's connection pool: %v", err)
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(0)
	pool.SetConnMaxIdleTime(0)
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing the handle: %v", err)
		}
	})
	applyMigration(t, handle, "sqlite/0001_settings.sql")
	bus := newRecordingBus()
	s := newSettingsStore(demoTable(t), stubDatabase{handle: handle, dialect: db.SQLite}, bus, moduleSettings{})
	s.logger = discardLogger()
	if err := s.load(context.Background()); err != nil {
		t.Fatalf("loading the snapshot: %v", err)
	}
	if err := s.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 3}); err != nil {
		t.Fatalf("seeding the row: %v", err)
	}
	return s, handle, bus
}

// insideTransaction runs call inside a transaction on the delivered handle and
// returns what call returned, failing the case if it has not returned within
// connectionTimer.
func insideTransaction(t *testing.T, handle *gorm.DB, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		var returned error
		// The transaction holds the pool's one connection from its begin on.
		err := handle.Transaction(func(*gorm.DB) error {
			returned = call()
			return nil
		})
		if err != nil {
			t.Errorf("the caller's own transaction failed: %v", err)
		}
		done <- returned
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(connectionTimer):
		t.Fatalf("the call inside the caller's transaction on a one-connection database was still "+
			"waiting after %v", connectionTimer)
		return nil
	}
}

// A write, a delete and a listing made inside the caller's own transaction on a
// database held to one connection give up on the connection instead of waiting
// for the caller, and leave the table as it was.
//
// None of them can join the caller's transaction, so each takes a connection of
// its own, and on such a database the only one is the caller's until the call
// returns. Under a context with no deadline nothing else ends that wait, and a
// write begins its transaction out of the caller's cancellation, so for a write
// no deadline would end it either; the database's one connection would stay
// held, and every other statement in the process would queue behind it. A
// database module delivers an in-memory SQLite database this way, and an
// in-process subscriber writing a setting inside its publisher's transaction is
// the ordinary path into the call.
func TestACallInsideTheCallersTransactionOnOneConnectionGivesUp(t *testing.T) {
	cases := []struct {
		name string
		call func(s *settingsStore, ctx context.Context) error
	}{
		{"a write", func(s *settingsStore, ctx context.Context) error {
			return s.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 7})
		}},
		{"a delete", func(s *settingsStore, ctx context.Context) error {
			return s.Delete(ctx, Global(), "demo.limit", "")
		}},
		{"a listing", func(s *settingsStore, ctx context.Context) error {
			_, err := s.List(ctx, Global())
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, handle, bus := storeOnOneConnection(t)
			published := len(bus.events())

			refused := insideTransaction(t, handle, func() error { return c.call(s, context.Background()) })
			if refused == nil || !strings.Contains(refused.Error(), "came free within") {
				t.Fatalf("the call returned %v, want a failure saying no connection came free", refused)
			}

			if got, err := Get[int](s, Global(), "demo.limit"); err != nil || got != 3 {
				t.Errorf("demo.limit reads %v (%v) after the refused call, want 3", got, err)
			}
			entries, err := s.List(context.Background(), Global())
			if err != nil || len(entries) != 1 || entries[0].Value != "3" {
				t.Errorf("the table holds %+v (%v) after the refused call, want the one row 3", entries, err)
			}
			if events := bus.events(); len(events) != published {
				t.Errorf("the refused call published %+v", events[published:])
			}
		})
	}
}

// A caller's deadline ends the wait for a connection when it falls due first.
//
// The wait comes before anything is written, so giving up there is what the
// caller's cancellation is for, a write's included: the write takes its
// connection under that cancellation and only begins its transaction out of it.
func TestACallersDeadlineEndsTheWaitForAConnection(t *testing.T) {
	const deadline = time.Second
	cases := []struct {
		name string
		call func(s *settingsStore, ctx context.Context) error
	}{
		{"a write", func(s *settingsStore, ctx context.Context) error {
			return s.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 7})
		}},
		{"a delete", func(s *settingsStore, ctx context.Context) error {
			return s.Delete(ctx, Global(), "demo.limit", "")
		}},
		{"a listing", func(s *settingsStore, ctx context.Context) error {
			_, err := s.List(ctx, Global())
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, handle, _ := storeOnOneConnection(t)
			started := time.Now()
			err := insideTransaction(t, handle, func() error {
				ctx, cancel := context.WithTimeout(context.Background(), deadline)
				defer cancel()
				return c.call(s, ctx)
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("the call returned %v, want the caller's deadline", err)
			}
			if took := time.Since(started); took >= connectionWait {
				t.Errorf("the call gave up after %v, past its caller's %v deadline", took, deadline)
			}
		})
	}
}

// A reload waiting for the connection a caller's transaction holds does not
// hold up that caller's write, and finishes once the transaction ends.
//
// A reload takes the reload lock and then the table, and the ticker's and a
// delivered event's reloads run whenever they fall due, a caller's
// transaction or not. A write that waited on that lock before taking its own
// connection would wait on a reload that waits on the caller: neither bound
// would reach it.
func TestAReloadWaitingForTheCallersConnectionHoldsNoWriteUp(t *testing.T) {
	s, handle, _ := storeOnOneConnection(t)
	reloaded := make(chan struct{})

	err := insideTransaction(t, handle, func() error {
		go func() {
			s.reload(context.Background())
			close(reloaded)
		}()
		for s.reloadMu.TryLock() {
			s.reloadMu.Unlock()
			time.Sleep(time.Millisecond)
		}
		return s.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 7})
	})
	if err == nil || !strings.Contains(err.Error(), "came free within") {
		t.Fatalf("the write returned %v, want a failure saying no connection came free", err)
	}
	select {
	case <-reloaded:
	case <-time.After(connectionTimer):
		t.Fatal("the reload was still waiting after the caller's transaction ended")
	}
	if got, err := Get[int](s, Global(), "demo.limit"); err != nil || got != 3 {
		t.Errorf("demo.limit reads %v (%v), want 3", got, err)
	}
}
