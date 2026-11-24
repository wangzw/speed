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

func (h *recorded) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recorded) WithGroup(string) slog.Handler      { return h }

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

func attrText(v slog.Value) string {
	if err, ok := v.Any().(error); ok {
		return err.Error()
	}
	if s, ok := v.Any().(string); ok {
		return s
	}
	return fmt.Sprint(v.Any())
}

func (h *recorded) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.held))
	for _, r := range h.held {
		out = append(out, r.Message)
	}
	return out
}

func captureLogs(r *roster) *recorded {
	rec := &recorded{}
	r.logger = slog.New(rec)
	return rec
}

func attrs(r slog.Record) []slog.Attr {
	var out []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		out = append(out, a)
		return true
	})
	return out
}

func rosterOverPeriod(t *testing.T, period time.Duration, rows ...string) *roster {
	t.Helper()
	r := rosterOver(t, rows...)
	r.refresh = period
	return r
}

// TestLoadEndsTheStartupWhenTheReadRefuses checks that a table holding a row the
// module will not act on makes load return the read's error.
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
	if lines := rec.carrying("tenants", "active", "disabled", "deleted"); len(lines) != 0 {
		t.Errorf("the failed load wrote %d records carrying the startup account's counts, want none: "+
			"the account is the success path's; the lines written were %v", len(lines), rec.messages())
	}
}

// TestLoadWritesTheStartupAccount checks that a successful load writes the
// startup account naming the snapshot's size and status distribution.
func TestLoadWritesTheStartupAccount(t *testing.T) {
	r := rosterOver(t,
		insert("acme", "acme"),
		insert("globex", "globex"),
		insert("initech", "initech"),
		insert("umbrella", "umbrella"),
		insert("hooli", "hooli"),
		insert("wonka", "wonka"),
	)
	if err := r.handle.Exec(
		`UPDATE tenants SET status = 0 WHERE id IN ('initech', 'umbrella')`).Error; err != nil {
		t.Fatalf("disabling two tenants: %v", err)
	}
	if err := r.handle.Exec(`UPDATE tenants SET status = 2 WHERE id = 'wonka'`).Error; err != nil {
		t.Fatalf("deleting a tenant: %v", err)
	}
	rec := captureLogs(r)

	if err := r.load(context.Background()); err != nil {
		t.Fatalf("loading a conforming roster: %v", err)
	}

	lines := rec.carrying("tenants", "active", "disabled", "deleted")
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
	want := map[string]int64{"tenants": 6, "active": 3, "disabled": 2, "deleted": 1}
	for _, key := range []string{"tenants", "active", "disabled", "deleted"} {
		if got[key] != want[key] {
			t.Errorf("the startup account's %s is %d, want the %s count %d: %v",
				key, got[key], key, want[key], got)
		}
	}
}

// TestRefreshOnceKeepsTheSnapshotAndSaysSo checks that a failed refresh keeps the
// published snapshot and logs the failure.
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
	if _, ok := after.byName["acme"]; !ok {
		t.Error("the previous snapshot no longer answers for the tenant it held")
	}

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

// TestRefreshOncePublishesAChangedTable checks that a refresh makes a tenant
// written after the load resolvable without a restart.
func TestRefreshOncePublishesAChangedTable(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	if _, ok, err := r.ByName(ctx, "globex"); err != nil || ok {
		t.Fatalf("the roster resolves a tenant the table does not hold: found=%v err=%v", ok, err)
	}

	if err := r.handle.Exec(insert("globex", "globex")).Error; err != nil {
		t.Fatalf("writing a tenant outside Put: %v", err)
	}
	r.refreshOnce(ctx)

	served, ok, err := r.ByName(ctx, "globex")
	if err != nil {
		t.Fatalf("resolving the tenant that was written: %v", err)
	}
	if !ok {
		t.Fatal("the refresh did not publish the table's new row, so the tenant stays unresolved " +
			"until a restart")
	}
	if served.ID != "globex" {
		t.Errorf("the name resolves to tenant %q, want the %q the table carries", served.ID, "globex")
	}
}

// TestStopIsIdempotentAndSafeBeforeStart checks that stop can be called twice
// and before start without panicking.
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

// TestAZeroPeriodStartsNoRefresher checks that a zero refresh period starts no
// refresher, leaving a tenant written after the load unresolved.
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
		if _, ok, err := r.ByName(ctx, "globex"); err != nil {
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

// TestAPeriodAboveZeroRereadsTheTable checks that a positive refresh period
// rereads the table and resolves a tenant written after the load.
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
		if _, ok, err := r.ByName(ctx, "globex"); err != nil {
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

// TestStopEndsTheRefresher checks that a stopped roster's refresher no longer
// rereads the table.
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
		if _, ok, err := r.ByName(ctx, "globex"); err != nil {
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

	// Stop does not wait for a refresh already in flight, so give one time to
	// land before writing the row below.
	time.Sleep(10 * period)

	if err := r.handle.Exec(insert("initech", "initech")).Error; err != nil {
		t.Fatalf("writing a tenant outside Put after the stop: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, ok, err := r.ByName(ctx, "initech"); err != nil {
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
