package token

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// createSessionTable makes the session table the sweep's condition joins to. The
// real table is the session module's, with more columns; the sweep reads only
// the identifier, the revocation and the absolute term, so those are what the
// fixture carries.
func (r *rig) createSessionTable() {
	r.t.Helper()
	err := r.p.handle.Exec("CREATE TABLE IF NOT EXISTS " + sessionTable +
		" (id TEXT PRIMARY KEY, revoked_at DATETIME NULL, expires_at DATETIME NULL)").Error
	if err != nil {
		r.t.Fatalf("creating the session table: %v", err)
	}
}

func (r *rig) seedSession(id string, revokedAt, expiresAt *time.Time) {
	r.t.Helper()
	err := r.p.handle.Exec("INSERT INTO "+sessionTable+" (id, revoked_at, expires_at) VALUES (?, ?, ?)",
		id, revokedAt, expiresAt).Error
	if err != nil {
		r.t.Fatalf("seeding session %s: %v", id, err)
	}
}

func (r *rig) seedRefresh(id, session string, idleExpiresAt time.Time) {
	r.t.Helper()
	err := r.p.handle.Exec("INSERT INTO "+tableName+
		" (id, session_id, token_hash, parent_id, issued_at, idle_expires_at, used_at, replaced_by) "+
		"VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL)",
		id, session, "hash-"+id, epoch, idleExpiresAt).Error
	if err != nil {
		r.t.Fatalf("seeding refresh row %s: %v", id, err)
	}
}

func (r *rig) countRefresh() int {
	r.t.Helper()
	var n int
	if err := r.p.handle.Raw("SELECT count(*) FROM " + tableName).Scan(&n).Error; err != nil {
		r.t.Fatalf("counting refresh rows: %v", err)
	}
	return n
}

func future() time.Time { return epoch.Add(24 * time.Hour) }
func past() time.Time   { return epoch.Add(-time.Hour) }

func ptr(t time.Time) *time.Time { return &t }

// TestTheSweepRemovesEachStaleConditionAndKeepsALiveRow holds the closed set of
// delete conditions: a row whose session is gone, revoked, or past its absolute
// term, or whose own idle window has passed, goes; a row on a live session
// within its idle window stays.
func TestTheSweepRemovesEachStaleConditionAndKeepsALiveRow(t *testing.T) {
	r := newRig(t)
	r.createSessionTable()

	r.seedSession("sess-live", nil, ptr(future()))
	r.seedSession("sess-revoked", ptr(epoch), ptr(future()))
	r.seedSession("sess-expired", nil, ptr(past()))

	r.seedRefresh("live", "sess-live", future())
	r.seedRefresh("revoked", "sess-revoked", future())
	r.seedRefresh("expired", "sess-expired", future())
	r.seedRefresh("idle", "sess-live", past())
	r.seedRefresh("ghost", "sess-nowhere", future())

	n, err := r.p.sweepOnce(context.Background())
	if err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	if n != 4 {
		t.Errorf("the sweep deleted %d rows, want 4 (revoked, expired, idle and ghost)", n)
	}
	if got := r.countRefresh(); got != 1 {
		t.Fatalf("the table holds %d rows, want the one live row", got)
	}
	var kept string
	if err := r.p.handle.Raw("SELECT id FROM " + tableName).Scan(&kept).Error; err != nil {
		t.Fatalf("reading the kept row: %v", err)
	}
	if kept != "live" {
		t.Errorf("the sweep kept %q, want the live row", kept)
	}
}

// TestTheSweepWorksOffMoreThanOneBatch holds the batch bound: a backlog larger
// than one batch is deleted across several statements in a single run.
func TestTheSweepWorksOffMoreThanOneBatch(t *testing.T) {
	r := newRig(t)
	r.createSessionTable()

	const stale = 2*sweepBatch + 500
	for i := range stale {
		r.seedRefresh(fmt.Sprintf("stale-%d", i), "sess-nowhere", past())
	}
	r.seedSession("sess-live", nil, ptr(future()))
	r.seedRefresh("live", "sess-live", future())

	n, err := r.p.sweepOnce(context.Background())
	if err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	if n != stale {
		t.Errorf("the sweep deleted %d rows, want %d", n, stale)
	}
	if got := r.countRefresh(); got != 1 {
		t.Errorf("the table holds %d rows, want the one live row", got)
	}
}

// TestTwoReplicasSweepingOnceDeleteNothingTwice holds that the conditional
// delete needs no coordination: two sweeps of the same table, run at once,
// delete a row once between them and neither errors.
func TestTwoReplicasSweepingOnceDeleteNothingTwice(t *testing.T) {
	r := newRig(t)
	r.createSessionTable()

	const stale = 2*sweepBatch + 500
	for i := range stale {
		r.seedRefresh(fmt.Sprintf("stale-%d", i), "sess-nowhere", past())
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	counts := make([]int, 2)
	errs := make([]error, 2)
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counts[i], errs[i] = r.p.sweepOnce(ctx)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d failed: %v", i, err)
		}
	}
	if got := counts[0] + counts[1]; got != stale {
		t.Errorf("the two replicas deleted %d rows between them, want %d", got, stale)
	}
	if got := r.countRefresh(); got != 0 {
		t.Errorf("the table holds %d rows, want none", got)
	}
}

// TestStartSweepIsBoundedAndStopEndsIt holds the sweep's shape: one goroutine
// per product, a second start is a no-op, and Stop ends it without waiting
// beyond the module's own bound.
func TestStartSweepIsBoundedAndStopEndsIt(t *testing.T) {
	if sweepInterval != time.Hour {
		t.Errorf("sweepInterval = %s, want 1h", sweepInterval)
	}
	if sweepBatch != 1000 {
		t.Errorf("sweepBatch = %d, want 1000", sweepBatch)
	}
	if stopWait != 5*time.Second {
		t.Errorf("stopWait = %s, want 5s", stopWait)
	}

	r := newRig(t)
	r.createSessionTable()
	r.p.startSweep(context.Background())
	// A second start of the same product must not launch a second goroutine.
	r.p.startSweep(context.Background())

	done := make(chan struct{})
	go func() {
		r.p.stopSweep()
		// After a stop, a further stop is a no-op and must not block.
		r.p.stopSweep()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopWait + 5*time.Second):
		t.Fatal("stopSweep did not return within its bound")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the condition was not met within the bound")
}

// TestTheSweepGoroutineRunsAndASecondStartAddsNone holds the design property
// the map-entry check cannot observe: one sweep goroutine per product,
// actually running, and a second start launching none.
func TestTheSweepGoroutineRunsAndASecondStartAddsNone(t *testing.T) {
	r := newRig(t)
	r.p.startSweep(context.Background())
	defer r.p.stopSweep()

	waitUntil(t, func() bool { return r.p.sweepRuns.Load() == 1 })
	r.p.startSweep(context.Background())
	if got := r.p.sweepRuns.Load(); got != 1 {
		t.Errorf("the sweep run counter is %d after a second start, want 1", got)
	}
}

// TestTheSweepNeverDeletesAProtectedRow holds the safety direction: rows on a
// live session whose idle window is open are never read by the delete, however
// many stale rows sit beside them.
func TestTheSweepNeverDeletesAProtectedRow(t *testing.T) {
	r := newRig(t)
	r.createSessionTable()
	r.seedSession("sess-live", nil, ptr(future()))

	for i := range 50 {
		r.seedRefresh(fmt.Sprintf("live-%d", i), "sess-live", future())
	}
	for i := range 50 {
		r.seedRefresh(fmt.Sprintf("ghost-%d", i), "sess-nowhere", past())
	}

	n, err := r.p.sweepOnce(context.Background())
	if err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	if n != 50 {
		t.Errorf("the sweep deleted %d rows, want the 50 stale ones", n)
	}
	if got := r.countRefresh(); got != 50 {
		t.Errorf("the table holds %d rows, want the 50 protected ones", got)
	}
}
