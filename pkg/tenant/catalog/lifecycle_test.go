package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// recorded is a slog.Handler that keeps the records written through it. The
// roster's startup account and its failed-refresh line are promises about what
// is written, and the record is the thing written: a handler of this shape lets
// a case read a line by the values it carries without depending on how a
// handler would render them, and without reading the level the line was written
// at, which is not what either promise is about.
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

// carrying returns the records holding every one of the named attributes. The
// startup account is the line that names the snapshot's size and its two
// halves, so a case about it reads the line by those names: what is promised is
// that the load writes the account, not that it writes it at one particular
// level.
func (h *recorded) carrying(keys ...string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.held {
		held := map[string]bool{}
		r.Attrs(func(a slog.Attr) bool {
			held[a.Key] = true
			return true
		})
		all := true
		for _, key := range keys {
			if !held[key] {
				all = false
				break
			}
		}
		if all {
			out = append(out, r.Clone())
		}
	}
	return out
}

// carryingText returns the records one of whose attributes carries want. A line
// that reports a failure carries that failure's text, and how it was handed
// over is not part of the promise: an error written under a name and that
// error's message written under the same name say the same thing to whoever
// reads the line.
func (h *recorded) carryingText(want string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.held {
		carries := false
		r.Attrs(func(a slog.Attr) bool {
			if strings.Contains(attrText(a.Value), want) {
				carries = true
				return false
			}
			return true
		})
		if carries {
			out = append(out, r.Clone())
		}
	}
	return out
}

// attrText is one attribute's value as the text a reader sees: an error is its
// message, a string is itself, and any other value is its default rendering.
func attrText(v slog.Value) string {
	if err, ok := v.Any().(error); ok {
		return err.Error()
	}
	if s, ok := v.Any().(string); ok {
		return s
	}
	return fmt.Sprint(v.Any())
}

// messages returns what the held records say, so that a case which expected one
// line and found none can show the lines it found instead.
func (h *recorded) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.held))
	for _, r := range h.held {
		out = append(out, r.Message)
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
//
// What the failed load must not write is the startup account, and the case
// reads for it by the names the account carries. Silence is not the promise — a
// failure path may write lines of its own — so the check is the account's
// content rather than the absence of a level.
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
	if lines := rec.carrying("tenants", "active", "disabled"); len(lines) != 0 {
		t.Errorf("the failed load wrote %d records carrying the startup account's counts, want none: "+
			"the account is the success path's; the lines written were %v", len(lines), rec.messages())
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
//
// The line is found by those names and not by a level. The promise is that the
// successful load writes the account; a release that moved the line to another
// level would still have written it, so a level is not something this case can
// hold the account to.
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

	lines := rec.carrying("tenants", "active", "disabled")
	if len(lines) != 1 {
		t.Fatalf("the successful load wrote %d records carrying the startup account's counts, want 1: %v",
			len(lines), rec.messages())
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
// The line is judged by what it carries, not by its wording, its level or the
// Go type of its attributes. It has to carry the failure itself, which is what
// lets an operator tell a refused read from a lost connection, so the case looks
// for that failure's text among the record's attributes: the error value and its
// message are the same text to a reader, and which one the code hands over is
// not something this promise fixes. The level is not part of it either — the
// bootstrap chain's level defaults to Info but configuration sets it
// (docs/design/modules/design-log.md) — so a line recorded at Debug is still the
// line that says the previous snapshot stays in force.
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

	// The failure the refresher had to report is the one this same read returns,
	// so the case reads it from the read rather than matching a fragment of it.
	refused := r.read(ctx)
	if refused == nil {
		t.Fatal("the read accepted the row it was made to refuse, so this case cannot say which " +
			"failure the refresher had to report")
	}
	if lines := rec.carryingText(refused.Error()); len(lines) == 0 {
		t.Errorf("the refused refresh wrote no record carrying the failure that refused the read "+
			"(%v); the lines written were %v", refused, rec.messages())
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

// TestStopEndsTheRefresher holds the refresher's end. The design fixes it, in
// docs/design/modules/design-tenant.md: the refresh goroutine is cancelled at
// Stop. The observable that follows is that the table is not reread afterwards —
// a tenant written once the roster has been stopped never becomes resolvable.
//
// The case establishes first that a refresher is running at all, so that a
// roster which never started one cannot pass for a roster whose refresher was
// stopped. That wait is the one TestAPeriodAboveZeroRereadsTheTable makes, over
// the same period, the same five-second deadline and the same poll interval: it
// writes a tenant after the load and observes it become resolvable.
//
// The second wait is TestAZeroPeriodStartsNoRefresher's wait, over the same
// five-second deadline and the same five-millisecond poll interval: a wait that
// catches a refresh there is long enough to catch one here, so a tenant that
// became resolvable within this deadline is what a refresher still ticking after
// Stop would look like. The case can refute such a refresher but cannot confirm
// its absence — a goroutine that has exited and one that ticks without ever
// rereading look the same — so only the negative observation is reported.
func TestStopEndsTheRefresher(t *testing.T) {
	const period = 20 * time.Millisecond
	r := rosterOverPeriod(t, period, insert("acme", "acme"))
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
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no refresher was running to stop: the tenant written after the load never became " +
				"resolvable")
		}
		time.Sleep(5 * time.Millisecond)
	}

	r.stop()

	// Stop does not wait for a refresh already in flight, so one is given time
	// to land before the row below is written: what the deadline that follows
	// measures is a refresher still ticking, not one finishing a read it had
	// already started.
	time.Sleep(10 * period)

	if err := r.handle.Exec(insert("initech", "initech")).Error; err != nil {
		t.Fatalf("writing a tenant outside Put after the stop: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, ok, err := r.ByAlias(ctx, "initech"); err != nil {
			t.Fatalf("resolving the tenant written after the stop: %v", err)
		} else if ok {
			t.Fatal("the roster reread the table after it was stopped: a tenant written after the " +
				"stop became resolvable, so the refresh goroutine outlived Stop")
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
