package catalog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// recorded is a slog.Handler that keeps the records written through it. The
// roster's startup account and its failed-refresh line are promises about what
// is written, and the record is the thing written: a handler of this shape lets
// a case read a line's level and the values it carries without depending on how
// a handler would render them.
type recorded struct {
	mu   sync.Mutex
	held []slog.Record
}

func (h *recorded) Enabled(context.Context, slog.Level) bool { return true }

func (h *recorded) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = append(h.held, r.Clone())
	return nil
}

// WithAttrs and WithGroup return the receiver. A record's own attributes are
// what these cases read, and the roster's logger carries no bound attribute of
// its own, so nothing is lost by not deriving a layer here.
func (h *recorded) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recorded) WithGroup(string) slog.Handler      { return h }

// at returns the records written at one level.
func (h *recorded) at(level slog.Level) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.held {
		if r.Level == level {
			out = append(out, r)
		}
	}
	return out
}

// atLeast returns the records written at one level or above. A line the process
// writes for an operator is judged by what it says, not by the exact level it
// was written at: the default chain shows info and above, so any level at or
// above info is a line an operator sees while a debug-only one is not.
func (h *recorded) atLeast(level slog.Level) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.held {
		if r.Level >= level {
			out = append(out, r)
		}
	}
	return out
}

// captureLogs points the roster's logger at a handler that keeps what it is
// written, and returns it.
func captureLogs(r *roster) *recorded {
	rec := &recorded{}
	r.logger = slog.New(rec)
	return rec
}

// values returns the values of a record's attributes, in order.
func values(r slog.Record) []any {
	var out []any
	r.Attrs(func(a slog.Attr) bool {
		out = append(out, a.Value.Any())
		return true
	})
	return out
}

// attrs returns a record's attributes as they were written, in order. The
// startup account names each count it carries, and a case about that line reads
// the names to hold which count is which.
func attrs(r slog.Record) []slog.Attr {
	var out []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		out = append(out, a)
		return true
	})
	return out
}

// carriesError reports whether one of the record's attributes is an error
// matching target. A line that reports a failure has to carry the failure
// itself, so that a caller reaches the same sentinel it would from the call.
func carriesError(r slog.Record, target error) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if err, ok := a.Value.Any().(error); ok && errors.Is(err, target) {
			found = true
			return false
		}
		return true
	})
	return found
}

// rosterOverPeriod is rosterOver with the refresh period set, so that a case
// about the refresher can start one.
func rosterOverPeriod(t *testing.T, period time.Duration, rows ...string) *roster {
	t.Helper()
	r := rosterOver(t, rows...)
	r.refresh = period
	return r
}

// TestLoadEndsTheStartupWhenTheReadRefuses holds load to its failure path: a
// table holding a row this module will not act on makes load return the read's
// error, and that error is what ends the module's Init. Returning nil instead
// would carry the startup on against an empty roster, which refuses every
// guarded request while telling the operator nothing about why.
//
// The row is written as a statement, the one route to the table Put does not
// police, so the read is what has to catch it.
func TestLoadEndsTheStartupWhenTheReadRefuses(t *testing.T) {
	r := rosterOver(t, insert("acme corp", "acme"))
	rec := captureLogs(r)

	err := r.load(context.Background())
	if err == nil {
		t.Fatal("the load returned nil for a table the read refuses, so the startup would continue " +
			"against an empty roster")
	}
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("the failure is %v, want the read's own error", err)
	}
	if got := len(rec.at(slog.LevelInfo)); got != 0 {
		t.Errorf("the failed load wrote %d startup account lines, want none: the account is the "+
			"success path's", got)
	}
}

// TestLoadWritesTheStartupAccount holds the success path's unconditional line.
// The module writes the snapshot's size and status distribution whenever the
// load succeeds, so that an assembly whose roster is not what the operator
// expected can be seen at startup without anyone asking the table afterwards.
//
// Each count is read under the name the module writes it under, because which
// count is which is what the promise is about: the distribution. The names are
// the module's own — "tenants" for the total, "active" and "disabled" for the
// two halves — so renaming one updates this case. The three values are made
// distinct here (3, 2, 1) so that a line which swapped two of them is told
// apart from a correct one; a case that compared the values as a bag could not
// tell the halves apart at all, since a line reporting the active count under
// the disabled name would carry the same three numbers and pass.
func TestLoadWritesTheStartupAccount(t *testing.T) {
	r := rosterOver(t,
		insert("acme", "acme"),
		insert("globex", "globex"),
		insert("initech", "initech"),
	)
	if err := r.handle.Exec(`UPDATE tenants SET status = 0 WHERE id = 'initech'`).Error; err != nil {
		t.Fatalf("disabling a tenant: %v", err)
	}
	rec := captureLogs(r)

	if err := r.load(context.Background()); err != nil {
		t.Fatalf("loading a conforming roster: %v", err)
	}

	lines := rec.at(slog.LevelInfo)
	if len(lines) != 1 {
		t.Fatalf("the successful load wrote %d startup account lines, want 1", len(lines))
	}
	got := map[string]int64{}
	for _, a := range attrs(lines[0]) {
		if n, ok := a.Value.Any().(int64); ok {
			got[a.Key] = n
		}
	}
	want := map[string]int64{"tenants": 3, "active": 2, "disabled": 1}
	for _, key := range []string{"tenants", "active", "disabled"} {
		if got[key] != want[key] {
			t.Errorf("the startup account's %s is %d, want the %s count %d: %v",
				key, got[key], key, want[key], got)
		}
	}
}

// TestRefreshOnceKeepsTheSnapshotAndSaysSo holds a failed refresh whole. The
// snapshot half follows from read: the reread refuses, so what stays published
// is the snapshot already in force. What read does not do is say anything, and
// a background reread that fails in silence leaves the process serving a roster
// the operator cannot see is stale — so the line is the refresher's own promise.
//
// The line is judged by what the default chain would show and by what it
// carries, not by its wording. It has to be written at a level an operator sees
// without turning debug logging on — the default chain shows info and above —
// and it has to carry the failure itself, so that the operator can tell a
// refused read from a lost connection. The level is read as "at least info"
// rather than as one exact name, because the promise is that the line is
// written, not that it is written at one particular level above info.
func TestRefreshOnceKeepsTheSnapshotAndSaysSo(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	before, err := r.current()
	if err != nil {
		t.Fatalf("the first read published nothing: %v", err)
	}
	rec := captureLogs(r)

	// A row the read will not act on, written outside Put, makes the reread
	// refuse the whole table.
	if bad := r.handle.Exec(`UPDATE tenants SET id = 'acme corp'`).Error; bad != nil {
		t.Fatalf("turning the row bad: %v", bad)
	}
	r.refreshOnce(ctx)

	after, err := r.current()
	if err != nil {
		t.Fatalf("the refused refresh dropped the snapshot: %v", err)
	}
	if after != before {
		t.Error("the refused refresh replaced the snapshot instead of leaving the previous one in place")
	}
	if _, ok := after.byAlias["acme"]; !ok {
		t.Error("the previous snapshot no longer answers for the tenant it held")
	}

	lines := rec.atLeast(slog.LevelInfo)
	if len(lines) != 1 {
		t.Fatalf("the refused refresh wrote %d lines the default chain would show, want the one that "+
			"says the previous snapshot stays in force", len(lines))
	}
	if !carriesError(lines[0], tenant.ErrInvalidTenant) {
		t.Errorf("the line does not carry the failure that refused the read: %v", values(lines[0]))
	}
}

// TestRefreshOncePublishesAChangedTable holds the refresher's whole point: a
// tenant written into the table by some other route — another replica, an
// operator's statement, an import — becomes resolvable without a restart. In
// production the period brings this about; the case calls the refresher once
// directly, so the promise is held by what the code decides rather than by a
// wait.
func TestRefreshOncePublishesAChangedTable(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	if _, ok, err := r.ByAlias(ctx, "globex"); err != nil || ok {
		t.Fatalf("the roster resolves a tenant the table does not hold: found=%v err=%v", ok, err)
	}

	if err := r.handle.Exec(insert("globex", "globex")).Error; err != nil {
		t.Fatalf("writing a tenant outside Put: %v", err)
	}
	r.refreshOnce(ctx)

	served, ok, err := r.ByAlias(ctx, "globex")
	if err != nil {
		t.Fatalf("resolving the tenant that was written: %v", err)
	}
	if !ok {
		t.Fatal("the refresh did not publish the table's new row, so the tenant stays unresolved " +
			"until a restart")
	}
	if served.ID != "globex" {
		t.Errorf("the alias resolves to tenant %q, want the %q the table carries", served.ID, "globex")
	}
}

// TestStopIsIdempotentAndSafeBeforeStart holds the two ways stop is reached
// that a bare channel close would not survive. A process that stops twice —
// two Stop callbacks, or a Stop after an error path already ran one — must not
// close the same channel a second time; and a roster whose refresher never
// started has no channel at all and must not dereference a nil. Neither is
// reachable by waiting: both are decided by whether the call returns or panics.
func TestStopIsIdempotentAndSafeBeforeStart(t *testing.T) {
	t.Run("stopped twice", func(t *testing.T) {
		r := rosterOverPeriod(t, time.Hour)
		r.start()
		t.Cleanup(r.stop)
		r.stop()
		r.stop()
	})
	t.Run("before start", func(t *testing.T) {
		r := rosterOver(t)
		r.stop()
	})
}

// TestAZeroPeriodStartsNoRefresher holds zero as a legitimate choice rather
// than a refresher that still runs and finds nothing: a deployment that asked
// for no refresh must not reread the table, and a tenant written after the load
// must stay unresolved.
//
// The observable is a wait, and the wait is calibrated against the sibling case
// that proves a refresher does reread: TestAPeriodAboveZeroRereadsTheTable
// writes a tenant after the load and polls for it under the same five-second
// deadline and the same poll interval, and observes it. A wait that catches a
// refresh there is long enough to catch one here, so the tenant resolving
// within that deadline is what a refresher started for a zero period would look
// like. The case can refute such a refresher but cannot confirm its absence — a
// refresher that started and never managed to reread looks the same as none —
// so only the positive observation is reported here.
func TestAZeroPeriodStartsNoRefresher(t *testing.T) {
	r := rosterOverPeriod(t, 0, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	r.start()
	t.Cleanup(r.stop)

	if err := r.handle.Exec(insert("globex", "globex")).Error; err != nil {
		t.Fatalf("writing a tenant outside Put: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, err := r.ByAlias(ctx, "globex"); err != nil {
			t.Fatalf("resolving the tenant that was written: %v", err)
		} else if ok {
			t.Fatal("a zero refresh period reread the table: the tenant written after the load became " +
				"resolvable, so a refresher was started for a deployment that asked for none")
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAPeriodAboveZeroRereadsTheTable is the one case here whose observable is
// a wait: the refresher is a goroutine, and a table written after the load
// becomes resolvable only once it has ticked. The wait can refute a refresher
// that never rereads — the tenant never becomes resolvable — but it cannot
// prove a working one, since a slow machine and a broken refresher look the
// same until the deadline. The deadline is therefore generous, and the roster
// is stopped before the case ends so the goroutine does not outlive it.
func TestAPeriodAboveZeroRereadsTheTable(t *testing.T) {
	r := rosterOverPeriod(t, 20*time.Millisecond, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	r.start()
	t.Cleanup(r.stop)

	if err := r.handle.Exec(insert("globex", "globex")).Error; err != nil {
		t.Fatalf("writing a tenant outside Put: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, err := r.ByAlias(ctx, "globex"); err != nil {
			t.Fatalf("resolving the tenant that was written: %v", err)
		} else if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresher never reread the table: the tenant written after the load never " +
				"became resolvable")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
