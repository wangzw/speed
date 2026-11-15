package dbsuite

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect is db.Connection, handed in by the engine's own cases: this package
// does not import pkg/db (see the package comment).
type Connect func(ctx context.Context, handle *gorm.DB, wait time.Duration, fn func(session *gorm.DB) error) error

// boundedWait is the bound the cases below give Connection, and answeredWithin
// how soon it has to have answered: long enough for a loaded machine, and far
// short of both the SQLite driver's default busy timeout and forever.
const (
	boundedWait    = 300 * time.Millisecond
	answeredWithin = 3 * time.Second
)

// ConnectionWait holds Connection, on a handle whose pool is held to one
// connection, to its bound on waiting for a connection: taken while the
// caller's own transaction holds that connection, it fails within its wait with
// an error that wraps context.DeadlineExceeded, and fn does not run; taken once
// that connection is free, fn runs on it and the connection goes back to the
// pool.
//
// Without the bound the call would wait for the transaction, which waits for
// the call, until the context ends — which, under context.Background, it never
// does. So the call runs on a goroutine of its own, and a call still waiting
// well past the bound fails the case rather than the test binary.
func ConnectionWait(t *testing.T, handle *gorm.DB, connect Connect) {
	t.Helper()
	var ran atomic.Bool
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT 1").Error; err != nil {
			return err
		}
		answer := make(chan error, 1)
		started := time.Now()
		go func() {
			answer <- connect(context.Background(), handle, boundedWait, func(*gorm.DB) error {
				ran.Store(true)
				return nil
			})
		}()
		select {
		case err := <-answer:
			if took := time.Since(started); took > answeredWithin {
				t.Errorf("Connection answered after %v, well past its wait of %v", took, boundedWait)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Connection inside the caller's transaction on a one-connection handle gave %v, "+
					"want an error wrapping context.DeadlineExceeded", err)
			}
		case <-time.After(answeredWithin):
			t.Errorf("Connection inside the caller's transaction on a one-connection handle was still "+
				"waiting after %v, with a wait of %v", answeredWithin, boundedWait)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the caller's transaction failed: %v", err)
	}
	if ran.Load() {
		t.Error("fn ran without a connection")
	}

	var ranFree atomic.Bool
	if err := connect(t.Context(), handle, boundedWait, func(session *gorm.DB) error {
		ranFree.Store(true)
		return session.Exec("SELECT 1").Error
	}); err != nil {
		t.Fatalf("Connection once the connection was free: %v", err)
	}
	if !ranFree.Load() {
		t.Error("Connection reported success without running fn")
	}
	requireConnectionBack(t, handle)
}

// requireConnectionBack checks that a statement on the handle gets a connection
// of its pool, which on a pool of one is the one Connection took.
func requireConnectionBack(t *testing.T, handle *gorm.DB) {
	t.Helper()
	settled, cancel := context.WithTimeout(t.Context(), answeredWithin)
	defer cancel()
	if err := handle.WithContext(settled).Exec("SELECT 1").Error; err != nil {
		t.Errorf("a statement on the handle after Connection returned: %v; the connection did not go "+
			"back to the pool", err)
	}
}

// LockWait holds a statement run through Connection, on a handle an engine's
// module delivered, to the engine's own lock wait: while another connection
// holds a lock the statement needs, the statement fails once the lock wait the
// handle's locator sets, lockWait, has passed — not before it, and not after
// the far longer wait Connection was given for its connection — and once the
// lock is given up the same statement goes through.
//
// other is a second pool on the same database, whose transaction holds the
// lock.
func LockWait(t *testing.T, handle, other *gorm.DB, lockWait time.Duration, connect Connect) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE lock_rows (id INTEGER PRIMARY KEY, n INTEGER)",
		"INSERT INTO lock_rows (id, n) VALUES (1, 0)",
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the table the case writes into: %v", err)
		}
	}
	holder := other.Begin()
	if err := holder.Error; err != nil {
		t.Fatalf("beginning the transaction that holds the lock: %v", err)
	}
	if err := holder.Exec("UPDATE lock_rows SET n = 1 WHERE id = 1").Error; err != nil {
		_ = holder.Rollback()
		t.Fatalf("taking the lock: %v", err)
	}

	const connectionWait = time.Minute
	update := func(session *gorm.DB) error {
		return session.Session(&gorm.Session{Logger: logger.Discard}).
			Exec("UPDATE lock_rows SET n = 2 WHERE id = 1").Error
	}
	answer := make(chan error, 1)
	started := time.Now()
	go func() { answer <- connect(context.Background(), handle, connectionWait, update) }()
	waiting := false
	select {
	case err := <-answer:
		took := time.Since(started)
		if err == nil {
			t.Error("the statement went through while another connection held the lock it needs")
		}
		if took < lockWait {
			t.Errorf("the statement failed after %v, before the lock wait of %v its locator sets", took, lockWait)
		}
	case <-time.After(lockWait + answeredWithin):
		waiting = true
		t.Errorf("the statement was still waiting for the lock after %v, with a lock wait of %v set by its "+
			"locator", lockWait+answeredWithin, lockWait)
	}

	if err := holder.Rollback().Error; err != nil {
		t.Fatalf("giving the lock up: %v", err)
	}
	if waiting {
		// The call still waiting takes the lock the one below needs once it
		// is given up, so it is let finish first, and it then holds the lock
		// to itself: its update is expected to go through.
		if err := <-answer; err != nil {
			t.Errorf("the statement left waiting, once the lock was given up: %v", err)
		}
	}
	if err := connect(t.Context(), handle, answeredWithin, update); err != nil {
		t.Errorf("the statement once the lock was given up: %v", err)
	}
}

// OnATransaction holds Connection, handed a transaction on a handle whose pool
// is held to one connection, to running fn on that transaction: fn reads what
// the transaction wrote and nothing is waited for. The transaction holds the
// pool's only connection, so a connection taken from the pool instead would be
// waited for until the bound ran out.
func OnATransaction(t *testing.T, handle *gorm.DB, connect Connect) {
	t.Helper()
	if err := handle.Exec("CREATE TABLE on_tx_rows (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table the case writes into: %v", err)
	}
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO on_tx_rows (id) VALUES (1)").Error; err != nil {
			return err
		}
		var rows int64
		started := time.Now()
		if err := connect(context.Background(), tx, boundedWait, func(session *gorm.DB) error {
			return session.Raw("SELECT count(*) FROM on_tx_rows").Scan(&rows).Error
		}); err != nil {
			t.Errorf("Connection on the caller's transaction gave %v, want fn run on it", err)
		}
		if rows != 1 {
			t.Errorf("fn read %d rows, want the one the transaction it was handed wrote", rows)
		}
		if took := time.Since(started); took >= boundedWait {
			t.Errorf("Connection on the caller's transaction took %v, want no wait at all", took)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the caller's transaction failed: %v", err)
	}
}

// errPanicked is what fn panics with in PanicReleasesTheConnection.
var errPanicked = errors.New("fn panicked")

// PanicReleasesTheConnection holds Connection to handing its connection back
// when fn panics under a context that never ends, whatever fn left open on it —
// the transaction GORM opens around a write, one fn began itself, a result set
// it never closed: the panic comes out of Connection, what the transaction wrote
// is rolled back, and the connection is back in the pool. Under a context that
// never ends, database/sql itself would end none of them, and handing the
// connection back would wait on them for good.
//
// The handle's pool must be held to one connection. On a database that belongs
// to its one connection — SQLite's :memory: — the table the case created is
// still there afterwards, so the connection was not dropped from the pool.
func PanicReleasesTheConnection(t *testing.T, handle *gorm.DB, connect Connect) {
	t.Helper()
	if err := handle.Exec("CREATE TABLE panic_rows (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table the case writes into: %v", err)
	}
	var armed atomic.Bool
	if err := handle.Callback().Create().After("gorm:create").Before("gorm:commit_or_rollback_transaction").
		Register("dbsuite:panic-after-create",
			func(tx *gorm.DB) {
				if armed.Load() && tx.Statement.Table == "panic_rows" {
					panic(errPanicked)
				}
			}); err != nil {
		t.Fatalf("registering the callback that panics: %v", err)
	}
	quiet := handle.Session(&gorm.Session{Logger: logger.Discard})
	for _, c := range []struct {
		name string
		fn   func(session *gorm.DB) error
	}{
		{"inside the transaction GORM opens around a write", func(session *gorm.DB) error {
			armed.Store(true)
			defer armed.Store(false)
			return session.Table("panic_rows").Create(map[string]any{"id": 1}).Error
		}},
		{"inside a transaction fn began", func(session *gorm.DB) error {
			tx := session.Begin()
			if err := tx.Exec("INSERT INTO panic_rows (id) VALUES (1)").Error; err != nil {
				return err
			}
			panic(errPanicked)
		}},
		{"with a result set fn left open", func(session *gorm.DB) error {
			rows, err := session.Raw("SELECT id FROM panic_rows").Rows()
			if err != nil {
				return err
			}
			leaveOpen(rows)
			panic(errPanicked)
		}},
		{"with a row fn never scanned", func(session *gorm.DB) error {
			if err := session.Raw("SELECT id FROM panic_rows").Row().Err(); err != nil {
				return err
			}
			panic(errPanicked)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			came := make(chan any, 1)
			go func() {
				defer func() { came <- recover() }()
				_ = connect(context.Background(), quiet, answeredWithin, c.fn)
			}()
			select {
			case got := <-came:
				if err, ok := got.(error); !ok || !errors.Is(err, errPanicked) {
					t.Fatalf("Connection gave %v, want fn's panic carried up to its caller", got)
				}
			case <-time.After(answeredWithin):
				t.Fatalf("Connection was still handing its connection back %v after fn panicked", answeredWithin)
			}
			var rows int64
			settled, cancel := context.WithTimeout(t.Context(), answeredWithin)
			defer cancel()
			if err := handle.WithContext(settled).Raw("SELECT count(*) FROM panic_rows").Scan(&rows).Error; err != nil {
				t.Fatalf("reading the table once fn had panicked: %v; the connection is not back in the pool, "+
					"or the database it held is gone", err)
			}
			if rows != 0 {
				t.Errorf("the table holds %d rows once fn panicked, want what fn wrote rolled back", rows)
			}
		})
	}
}

// ReturnReleasesTheConnection holds Connection to handing its connection back
// when fn returns normally under a context that never ends, whatever fn left
// open on it — a transaction it began and did not end, a result set it never
// closed: Connection comes back with fn's own answer, what the transaction
// wrote is rolled back, and the connection is back in the pool. Under a
// context that never ends, database/sql itself would end neither of them, and
// handing the connection back would wait on them for good.
//
// The handle's pool must be held to one connection, as for
// PanicReleasesTheConnection.
func ReturnReleasesTheConnection(t *testing.T, handle *gorm.DB, connect Connect) {
	t.Helper()
	if err := handle.Exec("CREATE TABLE return_rows (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table the case writes into: %v", err)
	}
	quiet := handle.Session(&gorm.Session{Logger: logger.Discard})
	for _, c := range []struct {
		name string
		fn   func(session *gorm.DB) error
	}{
		{"a transaction fn began and did not end", func(session *gorm.DB) error {
			return session.Begin().Exec("INSERT INTO return_rows (id) VALUES (1)").Error
		}},
		{"a result set fn left open", func(session *gorm.DB) error {
			rows, err := session.Raw("SELECT id FROM return_rows").Rows()
			if err != nil {
				return err
			}
			leaveOpen(rows)
			return nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			came := make(chan error, 1)
			go func() { came <- connect(context.Background(), quiet, answeredWithin, c.fn) }()
			select {
			case err := <-came:
				if err != nil {
					t.Fatalf("Connection gave %v, want the nil fn returned", err)
				}
			case <-time.After(answeredWithin):
				t.Fatalf("Connection was still handing its connection back %v after fn returned", answeredWithin)
			}
			var rows int64
			settled, cancel := context.WithTimeout(t.Context(), answeredWithin)
			defer cancel()
			if err := handle.WithContext(settled).Raw("SELECT count(*) FROM return_rows").Scan(&rows).Error; err != nil {
				t.Fatalf("reading the table once fn had returned: %v; the connection is not back in the pool, "+
					"or the database it held is gone", err)
			}
			if rows != 0 {
				t.Errorf("the table holds %d rows once fn returned, want what its open transaction wrote rolled back",
					rows)
			}
		})
	}
}

// SavepointRecovers holds a transaction fn runs through Connection to GORM's
// nested transactions: a nested transaction whose statement fails rolls back to
// its savepoint, and the outer transaction carries on and commits. PostgreSQL
// refuses every statement in a transaction a statement failed in until it is
// rolled back to the savepoint, so any statement Connection put ahead of that
// rollback would leave the outer transaction failed.
func SavepointRecovers(t *testing.T, handle *gorm.DB, connect Connect) {
	t.Helper()
	if err := handle.Exec("CREATE TABLE savepoint_rows (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table the case writes into: %v", err)
	}
	quiet := handle.Session(&gorm.Session{Logger: logger.Discard})
	err := connect(t.Context(), quiet, answeredWithin, func(session *gorm.DB) error {
		return session.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("INSERT INTO savepoint_rows (id) VALUES (1)").Error; err != nil {
				return err
			}
			nested := tx.Transaction(func(inner *gorm.DB) error {
				failed := inner.Exec("INSERT INTO savepoint_rows (id) VALUES (1)").Error
				// Time passes between the failure and the rollback to the
				// savepoint, as it does in any caller that looks at the error.
				time.Sleep(5 * time.Millisecond)
				return failed
			})
			if nested == nil {
				t.Error("the nested transaction's duplicate insert went through")
			}
			return tx.Exec("INSERT INTO savepoint_rows (id) VALUES (2)").Error
		})
	})
	if err != nil {
		t.Fatalf("the transaction whose nested transaction rolled back to its savepoint gave %v, want it "+
			"committed", err)
	}
	var ids []int64
	if err := handle.Raw("SELECT id FROM savepoint_rows ORDER BY id").Scan(&ids).Error; err != nil {
		t.Fatalf("reading the table back: %v", err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Errorf("the table holds %v, want the rows 1 and 2 the outer transaction wrote", ids)
	}
}

// leaveOpen is where a result set goes that a case leaves open on purpose.
func leaveOpen(*sql.Rows) {}
