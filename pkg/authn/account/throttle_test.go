package account

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func (h *harness) failuresOf(scope authn.ThrottleScope, key string) (int, bool) {
	h.t.Helper()
	var rows []struct{ Failures int }
	err := h.handle.Raw("SELECT failures FROM "+tableThrottles+" WHERE scope = ? AND key_hash = ?",
		string(scope), keyHash(key)).Scan(&rows).Error
	if err != nil {
		h.t.Fatalf("reading the failure count: %v", err)
	}
	if len(rows) == 0 {
		return 0, false
	}
	return rows[0].Failures, true
}

// The free attempts are free, the next engages the back-off, and the wait doubles up to the cap.
func TestTheFreeAttemptsAreFreeAndTheWaitDoubles(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const key = "alice"
		for i := 1; i <= defaults.ThrottleFreeAttempts; i++ {
			engaged, err := h.p.Fail(ctx, authn.ScopeIdentifier, key)
			if err != nil {
				t.Fatalf("failure %d: %v", i, err)
			}
			if engaged {
				t.Errorf("failure %d engaged the back-off, want only past the %d free ones", i, defaults.ThrottleFreeAttempts)
			}
			if wait, err := h.p.Check(ctx, authn.ScopeIdentifier, key); err != nil || wait != 0 {
				t.Errorf("after %d failures Check gave %v and %v, want no wait and no error", i, wait, err)
			}
		}
		engaged, err := h.p.Fail(ctx, authn.ScopeIdentifier, key)
		if err != nil || !engaged {
			t.Fatalf("the failure past the free ones gave engaged=%v, %v; want engaged", engaged, err)
		}
		if wait, err := h.p.Check(ctx, authn.ScopeIdentifier, key); err != nil || wait <= 0 || wait > 2*time.Second {
			t.Errorf("Check during the first back-off gave %v and %v, want about two seconds", wait, err)
		}
		if lines := h.logs.lines("reason_class", authn.ReasonThrottled); len(lines) == 0 {
			t.Errorf("the engagement wrote no line with reason_class=%s: %s", authn.ReasonThrottled, h.logs.String())
		}
	})
}

// Only the failure at the free attempts plus one engages the back-off; later failures are counted but report nothing again.
func TestOnlyTheFirstFailurePastTheFreeAttemptsEngages(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("repeating", "")
		meters := h.withMeters()
		engagements := func() int64 {
			return meters.counter(engagementsInstrument, map[string]string{scopeAttribute: string(authn.ScopeAccount)})
		}
		for range defaults.ThrottleFreeAttempts + 1 {
			h.p.fail(ctx, authn.ScopeAccount, "k", id)
		}
		if n := len(h.bus.of(authn.EventThrottleEngaged)); n != 1 {
			t.Fatalf("%d engagement events at the failure past the free attempts, want one", n)
		}
		if got := engagements(); got != 1 {
			t.Fatalf("the engagement counter is %d at the failure past the free attempts, want one", got)
		}

		if engaged, err := h.p.Fail(ctx, authn.ScopeAccount, "k"); err != nil || engaged {
			t.Errorf("a failure past the first engagement gave engaged=%v, %v; want it counted on without engaging", engaged, err)
		}
		h.p.fail(ctx, authn.ScopeAccount, "k", id)
		if n := len(h.bus.of(authn.EventThrottleEngaged)); n != 1 {
			t.Errorf("a failure past the first engagement published %d events, want the one kept", n)
		}
		if got := engagements(); got != 1 {
			t.Errorf("a failure past the first engagement counted %d, want one", got)
		}
	})
}

// A success clears the key, and only that key.
func TestASuccessClearsTheKeyAndNoOther(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		for range 3 {
			if _, err := h.p.Fail(ctx, authn.ScopeAccount, "one"); err != nil {
				t.Fatalf("failing: %v", err)
			}
			if _, err := h.p.Fail(ctx, authn.ScopeAccount, "two"); err != nil {
				t.Fatalf("failing: %v", err)
			}
		}
		if err := h.p.Succeed(ctx, authn.ScopeAccount, "one"); err != nil {
			t.Fatalf("clearing: %v", err)
		}
		if _, ok := h.failuresOf(authn.ScopeAccount, "one"); ok {
			t.Error("the cleared key still holds a row")
		}
		if n, ok := h.failuresOf(authn.ScopeAccount, "two"); !ok || n != 3 {
			t.Errorf("the other key holds %d (present %v), want its three failures left alone", n, ok)
		}
	})
}

// Once the window passes without a failure and the earned wait runs out, the count returns to one on the next failure.
func TestTheCountReturnsToZeroAfterTheWindow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const key = "drifter"
		for i := 0; i < 3; i++ {
			if _, err := h.p.Fail(ctx, authn.ScopeClient, key); err != nil {
				t.Fatalf("failing: %v", err)
			}
		}

		h.clock.advance(time.Minute)
		engaged, err := h.p.Fail(ctx, authn.ScopeClient, key)
		if err != nil || engaged {
			t.Fatalf("a failure within the window gave engaged=%v, %v; want it counted on", engaged, err)
		}
		if n, _ := h.failuresOf(authn.ScopeClient, key); n != 4 {
			t.Errorf("the count is %d within the window, want four", n)
		}
		h.clock.advance(defaults.ThrottleDecay + time.Minute)
		if _, err := h.p.Fail(ctx, authn.ScopeClient, key); err != nil {
			t.Fatalf("failing after the window: %v", err)
		}
		if n, _ := h.failuresOf(authn.ScopeClient, key); n != 1 {
			t.Errorf("the count is %d after the window, want it restarted at one", n)
		}
	})
}

// Concurrent failures are all counted: the increment is atomic on both engines.
func TestConcurrentFailuresAreAllCounted(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const callers = 16
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := h.p.Fail(ctx, authn.ScopeFactor, "rush"); err != nil {
					t.Errorf("a concurrent failure returned %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
		if n, ok := h.failuresOf(authn.ScopeFactor, "rush"); !ok || n != callers {
			t.Errorf("the count is %d (present %v) after %d failures together, want them all", n, ok, callers)
		}
	})
}

// A scope outside the six is refused, so counters never share a key space.
func TestAScopeOutsideTheSixIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		bad := authn.ThrottleScope("tenant")
		if _, err := h.p.Check(ctx, bad, "k"); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("Check gave %v, want ErrInvalidArgument", err)
		}
		if _, err := h.p.Fail(ctx, bad, "k"); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("Fail gave %v, want ErrInvalidArgument", err)
		}
		if err := h.p.Succeed(ctx, bad, "k"); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("Succeed gave %v, want ErrInvalidArgument", err)
		}
	})
}

// A state that cannot be read is an error, never a zero, so a caller that cannot tell does not verify.
func TestACheckThatCannotReadIsAnError(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.handle.Exec("DROP TABLE " + tableThrottles)
		if _, err := h.p.Check(context.Background(), authn.ScopeIdentifier, "k"); !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("Check over a missing table gave %v, want ErrUnavailable", err)
		}
	})
}
