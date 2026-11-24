package settings

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// connectionTimer is how long a case waits for a call that should give up on
// a connection within connectionWait, before reporting it as still waiting.
func connectionTimer() time.Duration {
	return connectionWait + coretest.Scale(3*time.Second)
}

// storeOnOneConnection builds a loaded store over the in-memory database the
// database module delivers for :memory:, which it holds to a single connection,
// and seeds demo.limit's global row with 3.
func storeOnOneConnection(t *testing.T) (*settingsStore, *gorm.DB, *recordingBus) {
	t.Helper()
	handle := quietly(dbtest.SQLite(t, ":memory:", nil).DB())
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
	limit := connectionTimer()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("the call inside the caller's transaction on a one-connection database was still "+
			"waiting after %v", limit)
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

// lockBusyTimeout is the busy timeout the locator in the case below sets: short
// of the driver's default and of connectionWait, so that a lock wait held to
// either is told apart from one held to the locator's.
const lockBusyTimeout = time.Second

// A write, a delete and a listing that meet a lock another connection holds on
// a SQLite file wait for it for the connection's busy timeout, the one the
// locator sets, whatever connectionWait is, then fail with SQLite's refusal and
// leave the table as it was. A lock is what the same call waits for on a file
// database when a caller holds the database in its own transaction;
// connectionWait bounds the wait for a connection only.
func TestACallThatMeetsALockWaitsForTheBusyTimeout(t *testing.T) {
	t.Parallel()
	cases := map[string]func(s *settingsStore, ctx context.Context) error{
		"a write": func(s *settingsStore, ctx context.Context) error {
			return s.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 7})
		},
		"a delete": func(s *settingsStore, ctx context.Context) error {
			return s.Delete(ctx, Global(), "demo.limit", "")
		},
		"a listing": func(s *settingsStore, ctx context.Context) error {
			_, err := s.List(ctx, Global())
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), "locked.db")
			handle := quietly(dbtest.SQLite(t, file+"?_pragma=busy_timeout(1000)", nil).DB())
			applyMigration(t, handle, "sqlite/0001_settings.sql")
			bus := newRecordingBus()
			s := storeOn(t, handle, db.SQLite, demoTable(t), bus)
			// A wrong implementation applies the connection wait where the
			// engine's busy timeout bounds the lock, so widening it makes that
			// implementation need an hour while the right one still gives up
			// after the locator's second. The upper bound is that unscaled second
			// plus two seconds through GATE_TIME_SCALE alone; the hour is the
			// wrong time stated to DeclaredAgainst.
			s.connectWithin = time.Hour
			if err := s.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 3}); err != nil {
				t.Fatalf("seeding the row: %v", err)
			}
			published := len(bus.events())

			other, err := dbtest.SQLite(t, file, nil).DB().DB()
			if err != nil {
				t.Fatalf("reaching the other pool: %v", err)
			}
			holder, err := other.Conn(context.Background())
			if err != nil {
				t.Fatalf("taking a connection of the other pool: %v", err)
			}
			if _, err = holder.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
				t.Fatalf("holding the database: %v", err)
			}
			done := make(chan error, 1)
			started := time.Now()
			go func() { done <- call(s, context.Background()) }()
			bound := connectionTimer()
			select {
			case gave := <-done:
				took := time.Since(started)
				if gave == nil || !strings.Contains(gave.Error(), "SQLITE_BUSY") {
					t.Errorf("%s gave %v while another connection held the database, want SQLite's refusal of "+
						"the lock", name, gave)
				}
				if took < lockBusyTimeout {
					t.Errorf("%s gave up after %v, before the busy timeout of %v", name, took, lockBusyTimeout)
				}
				if limit := coretest.DeclaredAgainst(t, lockBusyTimeout, 2*time.Second, time.Hour); took > limit {
					t.Errorf("%s gave up after %v, well past the busy timeout of %v", name, took, lockBusyTimeout)
				}
			case <-time.After(bound):
				t.Errorf("%s was still waiting for the lock after %v", name, bound)
				_, _ = holder.ExecContext(context.Background(), "ROLLBACK")
				<-done
			}
			_, _ = holder.ExecContext(context.Background(), "ROLLBACK")
			_ = holder.Close()

			entries, err := s.List(context.Background(), Global())
			if err != nil || len(entries) != 1 || entries[0].Value != "3" {
				t.Errorf("the table holds %+v (%v) after the call that gave up, want the one row 3", entries, err)
			}
			if events := bus.events(); len(events) != published {
				t.Errorf("the call that gave up published %+v", events[published:])
			}
		})
	}
}

// The store writes, deletes and lists over a handle the host opened itself
// with gorm.Open, as it does over the one the database module delivers: every
// call takes a connection of whatever pool the handle is on.
func TestTheStoreWorksOverAHandleTheHostOpened(t *testing.T) {
	handle, err := gorm.Open(sqlitedriver.Open(filepath.Join(t.TempDir(), "host.db")),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a handle of the host's own: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(handle) })
	applyMigration(t, handle, "sqlite/0001_settings.sql")
	s := storeOn(t, handle, db.SQLite, demoTable(t), newRecordingBus())
	ctx := context.Background()
	if err = s.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 7}); err != nil {
		t.Fatalf("Set over the host's handle: %v", err)
	}
	got, err := Get[int](s, Global(), "demo.limit")
	if err != nil || got != 7 {
		t.Fatalf("demo.limit reads %v (%v) over the host's handle, want 7", got, err)
	}
	entries, err := s.List(ctx, Global())
	if err != nil || len(entries) != 1 || entries[0].Value != "7" {
		t.Fatalf("List over the host's handle gave %+v (%v), want the one row 7", entries, err)
	}
	if err := s.Delete(ctx, Global(), "demo.limit", ""); err != nil {
		t.Fatalf("Delete over the host's handle: %v", err)
	}
	if entries, err := s.List(ctx, Global()); err != nil || len(entries) != 0 {
		t.Fatalf("List after the delete gave %+v (%v), want no row", entries, err)
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
	case <-time.After(connectionTimer()):
		t.Fatal("the reload was still waiting after the caller's transaction ended")
	}
	if got, err := Get[int](s, Global(), "demo.limit"); err != nil || got != 3 {
		t.Errorf("demo.limit reads %v (%v), want 3", got, err)
	}
}
