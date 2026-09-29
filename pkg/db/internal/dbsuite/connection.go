package dbsuite

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
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

// ConnectionWait holds Connection, on a handle an engine's module delivered,
// to its bound on waiting for a connection: taken while the caller's own
// transaction holds the handle's only connection, it fails within its wait
// with an error that wraps context.DeadlineExceeded, and fn does not run;
// taken once that connection is free, fn runs on it and the connection goes
// back to the pool.
//
// The handle must be held to one connection. Without the bound the call would
// wait for the transaction, which waits for the call, until the context ends —
// which, under context.Background, it never does. So the call runs on a
// goroutine of its own, and a call still waiting well past the bound fails the
// case rather than the test binary.
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
	settled, cancel := context.WithTimeout(t.Context(), answeredWithin)
	defer cancel()
	if err := handle.WithContext(settled).Exec("SELECT 1").Error; err != nil {
		t.Errorf("a statement on the handle after Connection returned: %v; the connection did not go "+
			"back to the pool", err)
	}
}

// LockWait holds Connection, on a handle an engine's module delivered, to its
// bound on waiting for a lock: while another connection holds a lock the
// statement needs, the statement fails within the bound, under a context that
// has none; once the lock is given up, the same statement goes through.
//
// other is a second pool on the same database, whose transaction holds the
// lock. SQLite waits for a lock for the connection's busy timeout whatever the
// context says, and PostgreSQL for as long as the context lasts, so without the
// bound the statement would wait five seconds on the one and for good on the
// other.
func LockWait(t *testing.T, handle, other *gorm.DB, connect Connect) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE bound_rows (id INTEGER PRIMARY KEY, n INTEGER)",
		"INSERT INTO bound_rows (id, n) VALUES (1, 0)",
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the table the case writes into: %v", err)
		}
	}
	holder := other.Begin()
	if err := holder.Error; err != nil {
		t.Fatalf("beginning the transaction that holds the lock: %v", err)
	}
	if err := holder.Exec("UPDATE bound_rows SET n = 1 WHERE id = 1").Error; err != nil {
		_ = holder.Rollback()
		t.Fatalf("taking the lock: %v", err)
	}

	update := func(session *gorm.DB) error {
		return session.Exec("UPDATE bound_rows SET n = 2 WHERE id = 1").Error
	}
	answer := make(chan error, 1)
	started := time.Now()
	go func() { answer <- connect(context.Background(), handle, boundedWait, update) }()
	waiting := false
	select {
	case err := <-answer:
		if took := time.Since(started); took > answeredWithin {
			t.Errorf("the statement waited %v for the lock, well past the wait of %v", took, boundedWait)
		}
		if err == nil {
			t.Error("the statement went through while another connection held the lock it needs")
		}
	case <-time.After(answeredWithin):
		waiting = true
		t.Errorf("the statement was still waiting for the lock after %v, with a wait of %v",
			answeredWithin, boundedWait)
	}

	if err := holder.Rollback().Error; err != nil {
		t.Fatalf("giving the lock up: %v", err)
	}
	if waiting {
		// The call still waiting takes the lock the one below needs once it
		// is given up, so it is let finish first.
		<-answer
	}
	if err := connect(t.Context(), handle, answeredWithin, update); err != nil {
		t.Errorf("the statement once the lock was given up: %v", err)
	}
}

// LockWaitAcrossStatements holds Connection, on a handle an engine's module
// delivered, to one bound over every statement fn issues: two statements that
// each meet a lock another connection holds end, together, once the wait has
// passed — the second failing as soon as it meets the lock — rather than after a
// wait each.
//
// other is a second pool on the same database, whose transaction holds the
// lock.
func LockWaitAcrossStatements(t *testing.T, handle, other *gorm.DB, connect Connect) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE across_rows (id INTEGER PRIMARY KEY, n INTEGER)",
		"INSERT INTO across_rows (id, n) VALUES (1, 0)",
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the table the case writes into: %v", err)
		}
	}
	holder := other.Begin()
	if err := holder.Error; err != nil {
		t.Fatalf("beginning the transaction that holds the lock: %v", err)
	}
	defer func() { _ = holder.Rollback() }()
	if err := holder.Exec("UPDATE across_rows SET n = 1 WHERE id = 1").Error; err != nil {
		t.Fatalf("taking the lock: %v", err)
	}

	var first, second error
	answer := make(chan error, 1)
	started := time.Now()
	go func() {
		answer <- connect(context.Background(), handle, sharedWait, func(session *gorm.DB) error {
			first = session.Exec("UPDATE across_rows SET n = 2 WHERE id = 1").Error
			second = session.Exec("UPDATE across_rows SET n = 3 WHERE id = 1").Error
			return errors.Join(first, second)
		})
	}()
	select {
	case <-answer:
		took := time.Since(started)
		if first == nil || second == nil {
			t.Errorf("the statements gave %v and %v while another connection held the lock they need",
				first, second)
		}
		if took < sharedWait {
			t.Errorf("the statements failed after %v, before their wait of %v had passed", took, sharedWait)
		}
		if limit := sharedWait + sharedWait/2; took > limit {
			t.Errorf("the two statements failed after %v, with a wait of %v: the second waited for the "+
				"lock as if the wait had been its own", took, sharedWait)
		}
	case <-time.After(answeredWithin):
		t.Errorf("the statements were still waiting after %v, with a wait of %v", answeredWithin, sharedWait)
		_ = holder.Rollback()
		<-answer
	}
}

// The waits the case below gives Connection: the whole of it, and how much of it
// the connection is held for before it comes free.
const (
	sharedWait     = time.Second
	connectionHeld = 600 * time.Millisecond
)

// LockWaitAfterConnectionWait holds Connection, on a handle an engine's module
// delivered, to one bound over both waits: a statement whose connection came
// free only after part of the wait, and which then meets a lock another
// connection holds, fails once the wait has passed since the call began — not
// sooner, and not after a second wait of the same length for the lock.
//
// The handle must be held to one connection, which the case holds for
// connectionHeld; other is a second pool on the same database, whose
// transaction holds the lock.
func LockWaitAfterConnectionWait(t *testing.T, handle, other *gorm.DB, connect Connect) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE shared_bound_rows (id INTEGER PRIMARY KEY, n INTEGER)",
		"INSERT INTO shared_bound_rows (id, n) VALUES (1, 0)",
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the table the case writes into: %v", err)
		}
	}
	holder := other.Begin()
	if err := holder.Error; err != nil {
		t.Fatalf("beginning the transaction that holds the lock: %v", err)
	}
	defer func() { _ = holder.Rollback() }()
	if err := holder.Exec("UPDATE shared_bound_rows SET n = 1 WHERE id = 1").Error; err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool: %v", err)
	}
	taken, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatalf("taking the handle's one connection: %v", err)
	}
	go func() {
		time.Sleep(connectionHeld)
		_ = taken.Close()
	}()

	answer := make(chan error, 1)
	started := time.Now()
	go func() {
		answer <- connect(context.Background(), handle, sharedWait, func(session *gorm.DB) error {
			return session.Exec("UPDATE shared_bound_rows SET n = 2 WHERE id = 1").Error
		})
	}()
	select {
	case err := <-answer:
		took := time.Since(started)
		if err == nil {
			t.Error("the statement went through while another connection held the lock it needs")
		}
		if took < sharedWait {
			t.Errorf("the statement failed after %v, before its wait of %v had passed", took, sharedWait)
		}
		if limit := sharedWait + connectionHeld/2; took > limit {
			t.Errorf("the statement failed after %v: the %v its connection took left the lock wait "+
				"%v, and it waited as if the whole wait were still to come", took, connectionHeld,
				sharedWait-connectionHeld)
		}
	case <-time.After(answeredWithin):
		t.Errorf("the statement was still waiting after %v, with a wait of %v", answeredWithin, sharedWait)
		_ = holder.Rollback()
		<-answer
	}
}
