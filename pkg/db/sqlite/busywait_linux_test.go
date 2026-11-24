//go:build linux

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A statement that meets a lock another connection holds waits for it for the busy timeout its
// locator sets even when the thread it waits on is signalled meanwhile.
func TestALockWaitLastsTheBusyTimeoutWhileItsThreadIsSignalled(t *testing.T) {
	const busyTimeout = time.Second
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "signalled.db")

	holding, err := sql.Open(driverName, file)
	if err != nil {
		t.Fatalf("opening the holding pool: %v", err)
	}
	t.Cleanup(func() { _ = holding.Close() })
	holder, err := holding.Conn(ctx)
	if err != nil {
		t.Fatalf("taking the holding connection: %v", err)
	}
	if _, err = holder.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("taking the write lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = holder.ExecContext(ctx, "ROLLBACK")
		_ = holder.Close()
	})

	waiting, err := sql.Open(driverName, file+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatalf("opening the waiting pool: %v", err)
	}
	t.Cleanup(func() { _ = waiting.Close() })
	waiter, err := waiting.Conn(ctx)
	if err != nil {
		t.Fatalf("taking the waiting connection: %v", err)
	}
	t.Cleanup(func() { _ = waiter.Close() })

	type outcome struct {
		err  error
		took time.Duration
	}
	threads := make(chan int, 1)
	done := make(chan outcome, 1)
	go func() {
		// The statement runs on this goroutine, and the goroutine stays on
		// this thread, so the signals below reach the thread that sleeps.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		threads <- syscall.Gettid()
		started := time.Now()
		_, err := waiter.ExecContext(ctx, "BEGIN IMMEDIATE")
		done <- outcome{err, time.Since(started)}
	}()
	thread, process := <-threads, syscall.Getpid()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	limit := time.After(busyTimeout + 5*time.Second)
	for {
		select {
		case got := <-done:
			if got.err == nil || !strings.Contains(got.err.Error(), "SQLITE_BUSY") {
				t.Fatalf("the BEGIN gave %v while another connection held the write lock, want SQLite's "+
					"refusal of the lock", got.err)
			}
			if got.took < busyTimeout {
				t.Fatalf("the BEGIN gave up after %v while its thread was signalled, before the busy timeout of %v",
					got.took, busyTimeout)
			}
			if bound := busyTimeout + 2*time.Second; got.took > bound {
				t.Fatalf("the BEGIN gave up after %v, well past the busy timeout of %v", got.took, busyTimeout)
			}
			return
		case <-tick.C:
			_ = syscall.Tgkill(process, thread, syscall.SIGURG)
		case <-limit:
			t.Fatalf("the BEGIN was still waiting for the lock after %v", busyTimeout+5*time.Second)
		}
	}
}
