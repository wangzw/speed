package budget

import (
	"context"
	"testing"
	"time"
)

// TestTheLedgerModuleRunsARetentionSweep holds that the module that owns the
// table is the one that prunes it. PruneBefore existed with no caller, so
// sms_budget_events grew without bound; the sweep is the behaviour that keeps it
// bounded, and it lives on the descriptor's Start and Stop.
func TestTheLedgerModuleRunsARetentionSweep(t *testing.T) {
	m := Module()
	if m.Start == nil {
		t.Fatal("the ledger module starts no retention sweep: nothing prunes sms_budget_events")
	}
	if m.Stop == nil {
		t.Fatal("the ledger module declares no Stop for its retention sweep")
	}
}

// TestTheRetentionWindowIsTheDocumentedOne pins how long a row stays: the design
// states twenty-five hours — the longest budget window, one day, plus an hour of
// margin. The boundary case derives its cutoff from the same constant, so it
// passes whatever the constant is; this case names the window independently, so
// shrinking it below the consumers' longest window fails here.
func TestTheRetentionWindowIsTheDocumentedOne(t *testing.T) {
	const documented = 25 * time.Hour
	if pruneRetention < documented {
		t.Errorf("the retention window is %s, shorter than the documented minimum %s", pruneRetention, documented)
	}
	if pruneRetention != documented {
		t.Errorf("the retention window is %s, want the documented %s", pruneRetention, documented)
	}
}

// TestTheSweepRemovesOnlyRowsPastTheRetentionWindow holds the cutoff the module
// derives: a row attempted before now minus the retention window goes, a row at
// the boundary or inside the window stays, so no count over a budget window is
// under-counted.
func TestTheSweepRemovesOnlyRowsPastTheRetentionWindow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		now := at
		cutoff := now.Add(-pruneRetention)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-old", "T1", cutoff.Add(-time.Minute)))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-boundary", "T1", cutoff))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-inside", "T1", cutoff.Add(time.Minute)))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-recent", "T1", now.Add(-time.Hour)))

		sweep := newLedger(store)
		sweep.now = func() time.Time { return now }
		removed, err := sweep.prune(context.Background())
		if err != nil {
			t.Fatalf("pruning: %v", err)
		}
		if removed != 1 {
			t.Errorf("the sweep removed %d rows, want 1 (only the row strictly before the window)", removed)
		}

		all := Query{Since: now.Add(-48 * time.Hour), IncludeRefunded: true, IncludeSuppressed: true}
		assertCount(t, count(t, store, all), 3, all)
		if got := count(t, store, Query{Dimension: DimensionDestination, Value: "D-old", Since: all.Since}); got != 0 {
			t.Errorf("the row past the window survived: the destination count is %d, want 0", got)
		}
		for _, destination := range []string{"D-boundary", "D-inside", "D-recent"} {
			q := Query{Dimension: DimensionDestination, Value: destination, Since: all.Since}
			if got := count(t, store, q); got != 1 {
				t.Errorf("the sweep removed the %s row, which is inside the window", destination)
			}
		}
	})
}

// TestTheSweepOfAnEmptyLedgerReportsNothing holds that a ledger with no rows is
// swept cleanly: nothing to delete is an answer of zero, not an error, so an
// empty table does not turn the periodic job into a stream of warnings.
func TestTheSweepOfAnEmptyLedgerReportsNothing(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		sweep := newLedger(store)
		sweep.now = func() time.Time { return at }
		removed, err := sweep.prune(context.Background())
		if err != nil {
			t.Fatalf("pruning an empty ledger: %v", err)
		}
		if removed != 0 {
			t.Errorf("the sweep of an empty ledger removed %d rows, want 0", removed)
		}
		all := Query{Since: at.Add(-48 * time.Hour), IncludeRefunded: true, IncludeSuppressed: true}
		assertCount(t, count(t, store, all), 0, all)
	})
}

// drivenLedger builds a ledger whose clock is fixed and whose interval is short
// enough for a case to watch. The interval is a field so a case drives the sweep
// without waiting the default hour.
func drivenLedger(store Store, now time.Time) *ledger {
	l := newLedger(store)
	l.now = func() time.Time { return now }
	l.every = 20 * time.Millisecond
	l.stopBound = 2 * time.Second
	return l
}

// TestTheSweepRunsOnItsInterval holds the periodic wiring rather than the
// cutoff: after the startup sweep completes, a row written past the window is
// removed by a later tick of the injected interval. The startup sweep is proved
// finished first, so only the ticker can remove the row the case inserts.
func TestTheSweepRunsOnItsInterval(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		now := at
		contextAt := now.Add(-48 * time.Hour)
		sweep := drivenLedger(store, now)

		// A row the startup sweep removes proves that sweep has run before the
		// case writes the row only the interval can find.
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-startup", "T1", now.Add(-pruneRetention-time.Minute)))
		sweep.start(context.Background())
		defer func() {
			if err := sweep.stop(); err != nil {
				t.Errorf("stopping the sweep: %v", err)
			}
		}()
		awaitPruned(t, store, contextAt, "D-startup")

		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-ticked", "T1", now.Add(-pruneRetention-time.Minute)))
		awaitPruned(t, store, contextAt, "D-ticked")
	})
}

// TestTheModuleStartBeginsTheSweepAndStopEndsIt covers the descriptor's wiring
// rather than the sweep method: the row is past the window before Start runs,
// and only Start or Stop can remove it or end the goroutine. Start sweeps once
// as it begins, so the row goes without waiting for a tick; Stop has to return
// with the goroutine finished, or the process leaves one behind.
func TestTheModuleStartBeginsTheSweepAndStopEndsIt(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		now := at
		contextAt := now.Add(-48 * time.Hour)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D-started", "T1", now.Add(-pruneRetention-time.Minute)))

		m := Module()
		if m.Start == nil || m.Stop == nil {
			t.Fatal("the ledger module declares no Start and Stop for its retention sweep")
		}
		product := drivenLedger(store, now)
		if err := m.Start(context.Background(), nil, product); err != nil {
			t.Fatalf("starting the module: %v", err)
		}
		awaitPruned(t, store, contextAt, "D-started")
		if err := m.Stop(context.Background(), nil, product); err != nil {
			t.Fatalf("stopping the module: %v", err)
		}
		sweep := product.sweep
		if sweep == nil {
			t.Fatal("the module started no sweep to stop")
		}
		select {
		case <-sweep.done:
		default:
			t.Error("Stop returned while the sweep goroutine was still running")
		}
	})
}

// awaitPruned fails the case if destination is still in the ledger after a
// short bound, which gives the goroutine several intervals to tick.
func awaitPruned(t *testing.T, store Store, since time.Time, destination string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		q := Query{Dimension: DimensionDestination, Value: destination, Since: since}
		if count(t, store, q) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s row was not pruned within the bound", destination)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
