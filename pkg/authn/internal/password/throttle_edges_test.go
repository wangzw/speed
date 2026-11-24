package password

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheBackoffActsOnTheDefaultsWhenItsParametersAreOutsideTheirDomains holds
// that an out-of-domain parameter reads as its default.
func TestTheBackoffActsOnTheDefaultsWhenItsParametersAreOutsideTheirDomains(t *testing.T) {
	for name, build := range map[string]func(*clockAt) *Throttle{
		"the zero value": func(c *clockAt) *Throttle {
			return NewThrottle(func() ThrottleParams { return ThrottleParams{} }, c.now)
		},
		"no reader": func(c *clockAt) *Throttle { return NewThrottle(nil, c.now) },
	} {
		clock := newClock()
		th := build(clock)
		if engaged := failN(t, th, "k", 5); engaged != 0 {
			t.Errorf("%s: a failure within the five free attempts engaged the back-off", name)
		}
		if d := wait(t, th, "k"); d != 0 {
			t.Errorf("%s: the key waits %s after five failures", name, d)
		}
		if engaged := failN(t, th, "k", 1); engaged != 1 {
			t.Errorf("%s: the sixth failure did not engage the back-off", name)
		}
		if d := wait(t, th, "k"); d != 2*time.Second {
			t.Errorf("%s: the key waits %s after six failures, want 2s", name, d)
		}
	}
}

// TestNormalizedKeepsTheEdgesOfItsDomainsAndNothingOutside: the edges of each domain are in it and one step outside is not.
func TestNormalizedKeepsTheEdgesOfItsDomainsAndNothingOutside(t *testing.T) {
	d := DefaultThrottleParams()
	for _, in := range []ThrottleParams{
		{FreeAttempts: 1, Window: time.Minute, MaxBackoff: 10 * time.Second},
		{FreeAttempts: 20, Window: 24 * time.Hour, MaxBackoff: 24 * time.Hour},
	} {
		if got := in.Normalized(); got != in {
			t.Errorf("%+v, which is at the edges of the domains, normalises to %+v", in, got)
		}
	}
	ok := ThrottleParams{FreeAttempts: 3, Window: 2 * time.Minute, MaxBackoff: time.Minute}
	for name, c := range map[string]struct {
		in   ThrottleParams
		want ThrottleParams
	}{
		"free attempts 0":  {ThrottleParams{0, ok.Window, ok.MaxBackoff}, ThrottleParams{d.FreeAttempts, ok.Window, ok.MaxBackoff}},
		"free attempts 21": {ThrottleParams{21, ok.Window, ok.MaxBackoff}, ThrottleParams{d.FreeAttempts, ok.Window, ok.MaxBackoff}},
		"window under":     {ThrottleParams{ok.FreeAttempts, time.Minute - 1, ok.MaxBackoff}, ThrottleParams{ok.FreeAttempts, d.Window, ok.MaxBackoff}},
		"window over":      {ThrottleParams{ok.FreeAttempts, 24*time.Hour + 1, ok.MaxBackoff}, ThrottleParams{ok.FreeAttempts, d.Window, ok.MaxBackoff}},
		"cap under":        {ThrottleParams{ok.FreeAttempts, ok.Window, 10*time.Second - 1}, ThrottleParams{ok.FreeAttempts, ok.Window, d.MaxBackoff}},
		"cap over":         {ThrottleParams{ok.FreeAttempts, ok.Window, 24*time.Hour + 1}, ThrottleParams{ok.FreeAttempts, ok.Window, d.MaxBackoff}},
	} {
		if got := c.in.Normalized(); got != c.want {
			t.Errorf("%s: normalises to %+v, want %+v", name, got, c.want)
		}
	}
}

// TestAFixedKeysCountDecaysAfterTheWindow: a fixed key is never swept, so its
// count has to decay on its own.
func TestAFixedKeysCountDecaysAfterTheWindow(t *testing.T) {
	p := ThrottleParams{FreeAttempts: 1, Window: time.Minute, MaxBackoff: time.Hour}
	fixed := func(clock *clockAt) *Throttle {
		return NewThrottle(func() ThrottleParams { return p }, clock.now, Key{Scope: authn.ScopeIdentifier, Key: "fixed"})
	}

	t.Run("one nanosecond short, the count stands", func(t *testing.T) {
		clock := newClock()
		th := fixed(clock)
		failN(t, th, "fixed", 5)
		if d := wait(t, th, "fixed"); d != 16*time.Second {
			t.Fatalf("the fixed key waits %s after five failures, want 16s", d)
		}
		clock.advance(time.Minute - time.Nanosecond)
		failN(t, th, "fixed", 1)
		if d := wait(t, th, "fixed"); d != 32*time.Second {
			t.Errorf("the fixed key waits %s after a sixth failure, want 32s: the count of a fixed key was forgotten early", d)
		}
	})

	t.Run("at the window, the count is forgotten", func(t *testing.T) {
		clock := newClock()
		th := fixed(clock)
		failN(t, th, "fixed", 5)
		clock.advance(time.Minute)
		if engaged := failN(t, th, "fixed", 1); engaged != 0 {
			t.Error("a failure of a fixed key after the window was counted on top of the old count")
		}
		if d := wait(t, th, "fixed"); d != 0 {
			t.Errorf("the fixed key waits %s after the first failure of a new count", d)
		}
		if engaged := failN(t, th, "fixed", 1); engaged != 1 {
			t.Error("the second failure of the new count did not engage the back-off")
		}
	})
}

// TestAClearedFixedKeyHasItsFreeAttemptsBack: a success clears a fixed key to zero, not to one.
func TestAClearedFixedKeyHasItsFreeAttemptsBack(t *testing.T) {
	p := ThrottleParams{FreeAttempts: 2, Window: time.Hour, MaxBackoff: time.Minute}
	th := NewThrottle(func() ThrottleParams { return p }, newClock().now, Key{Scope: authn.ScopeIdentifier, Key: "fixed"})

	if engaged := failN(t, th, "fixed", 3); engaged != 1 {
		t.Fatalf("three failures engaged the back-off %d times, want once", engaged)
	}
	if err := th.Succeed(bg, authn.ScopeIdentifier, "fixed"); err != nil {
		t.Fatal(err)
	}
	if engaged := failN(t, th, "fixed", 2); engaged != 0 {
		t.Error("the two free attempts of a cleared fixed key were not both free")
	}
	if engaged := failN(t, th, "fixed", 1); engaged != 1 {
		t.Error("the first failure beyond the free attempts of a cleared fixed key did not engage the back-off")
	}
}

// TestAFailureAloneReclaimsKeysThatWentAWholeWindow: a failure alone, with no check before it, reclaims the keys that went a
// whole window, at the instant the window ends.
func TestAFailureAloneReclaimsKeysThatWentAWholeWindow(t *testing.T) {
	clock := newClock()
	p := ThrottleParams{FreeAttempts: 1, Window: 10 * time.Minute, MaxBackoff: time.Hour}
	th := NewThrottle(func() ThrottleParams { return p }, clock.now)
	for i := range 100 {
		failN(t, th, "old-"+strconv.Itoa(i), 1)
	}
	clock.advance(10*time.Minute - time.Nanosecond)
	failN(t, th, "new", 1)
	if got := th.Len(); got != 101 {
		t.Fatalf("one nanosecond before the window the table holds %d keys, want the 100 old and the new one", got)
	}
	clock.advance(time.Nanosecond)
	failN(t, th, "newer", 1)
	if got := th.Len(); got != 2 {
		t.Errorf("a whole window after the old keys the table holds %d keys, want the two that are within it", got)
	}
}

// TestChecksFailuresAndSuccessesRunTogetherWithoutARace: checks, failures and successes of one key run together without a race.
func TestChecksFailuresAndSuccessesRunTogetherWithoutARace(t *testing.T) {
	th := NewThrottle(DefaultThrottleParams, nil, Key{Scope: authn.ScopeIdentifier, Key: "fixed"})
	keys := []string{"fixed", "a", "b"}
	var wg sync.WaitGroup
	for g := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 40 {
				k := keys[(g+i)%len(keys)]
				switch (g + i) % 3 {
				case 0:
					_, _ = th.Check(bg, authn.ScopeIdentifier, k)
				case 1:
					_, _ = th.Fail(bg, authn.ScopeIdentifier, k)
				default:
					_ = th.Succeed(bg, authn.ScopeIdentifier, k)
				}
			}
		}()
	}
	wg.Wait()
	_ = th.Len()
}

// TestTwoPairsWhoseConcatenationsAreEqualAreTwoCounters: two scope and key pairs whose concatenations are equal are two counters.
func TestTwoPairsWhoseConcatenationsAreEqualAreTwoCounters(t *testing.T) {
	th := throttleAt(newClock(), DefaultThrottleParams())
	for range 8 {
		if _, err := th.Fail(bg, authn.ThrottleScope("a"), "bc"); err != nil {
			t.Fatal(err)
		}
	}
	if d, _ := th.Check(bg, authn.ThrottleScope("a"), "bc"); d == 0 {
		t.Fatal("the failures did not hold their own key back")
	}
	if d, _ := th.Check(bg, authn.ThrottleScope("ab"), "c"); d != 0 {
		t.Errorf("failures under the pair (a, bc) hold back the pair (ab, c) for %s: the scope and the key run together", d)
	}
}
