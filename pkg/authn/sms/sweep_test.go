package sms

import (
	"context"
	"testing"
	"time"
)

// TestTheSweepRemovesOnlyRowsPastTheWindow checks that the sweep removes only rows past the window.
func TestTheSweepRemovesOnlyRowsPastTheWindow(t *testing.T) {
	r := newRigSQLite(t)
	old := r.clock.read().Add(-sweepAfter - time.Hour)
	recent := r.clock.read().Add(-time.Hour)
	r.insert("old", purposeLogin, "m-old", nil, false, "mac", 0, 5, old.Add(-time.Minute), old, nil)
	r.insert("recent", purposeLogin, "m-recent", nil, false, "mac", 0, 5, recent.Add(-time.Minute), recent, nil)

	removed, err := r.p.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Errorf("the sweep removed %d rows, want 1", removed)
	}
	if _, ok := r.column("old", "id"); ok {
		t.Error("a row past the window survived the sweep")
	}
	if _, ok := r.column("recent", "id"); !ok {
		t.Error("a row inside the window was removed by the sweep")
	}
}

// TestTheSweepKeepsARowInsideTheDailyWindow checks that the sweep keeps a row inside the daily window.
func TestTheSweepKeepsARowInsideTheDailyWindow(t *testing.T) {
	r := newRigSQLite(t)
	now := r.clock.read()
	inside := now.Add(-23 * time.Hour)
	r.insert("inside", purposeLogin, "m-inside", nil, false, "mac", 0, 5,
		inside.Add(-5*time.Minute), inside, nil)
	outside := now.Add(-26 * time.Hour)
	r.insert("outside", purposeLogin, "m-outside", nil, false, "mac", 0, 5,
		outside.Add(-5*time.Minute), outside, nil)

	removed, err := r.p.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Errorf("the sweep removed %d rows, want 1", removed)
	}
	if _, ok := r.column("inside", "id"); !ok {
		t.Error("a row inside the daily window was removed by the sweep")
	}
	if _, ok := r.column("outside", "id"); ok {
		t.Error("a row past the margin survived the sweep")
	}
}

// TestTheSweepLeavesASpentRowUntilItsWindowCloses checks that the sweep leaves a spent row until its window closes.
func TestTheSweepLeavesASpentRowUntilItsWindowCloses(t *testing.T) {
	r := newRigSQLite(t)
	at := r.clock.read().Add(-time.Minute)
	r.insert("spent", purposeLogin, "m", nil, false, "mac", 1, 5, at, r.clock.read().Add(time.Minute), &at)

	if _, err := r.p.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, ok := r.column("spent", "id"); !ok {
		t.Error("a spent row inside the window was removed")
	}
}
