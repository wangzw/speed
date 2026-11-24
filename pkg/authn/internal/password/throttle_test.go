package password

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func throttleAt(clock *clockAt, p ThrottleParams) *Throttle {
	return NewThrottle(func() ThrottleParams { return p }, clock.now)
}

var bg = context.Background()

func failN(t *testing.T, th *Throttle, key string, n int) (engaged int) {
	t.Helper()
	for range n {
		e, err := th.Fail(bg, authn.ScopeIdentifier, key)
		if err != nil {
			t.Fatalf("recording a failure: %v", err)
		}
		if e {
			engaged++
		}
	}
	return engaged
}

func wait(t *testing.T, th *Throttle, key string) time.Duration {
	t.Helper()
	d, err := th.Check(bg, authn.ScopeIdentifier, key)
	if err != nil {
		t.Fatalf("checking a key: %v", err)
	}
	return d
}

// TestTheFreeAttemptsAreFreeAndTheNextOneEngagesTheBackoff checks the sixth
// failure engages a 2s wait while the fifth does not.
func TestTheFreeAttemptsAreFreeAndTheNextOneEngagesTheBackoff(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, DefaultThrottleParams())

	if engaged := failN(t, th, "k", 5); engaged != 0 {
		t.Errorf("a failure within the free attempts reported engaging the back-off")
	}
	if d := wait(t, th, "k"); d != 0 {
		t.Errorf("the key waits %s after its free attempts, want none", d)
	}
	engaged, _ := th.Fail(bg, authn.ScopeIdentifier, "k")
	if !engaged {
		t.Error("the first failure beyond the free attempts did not report engaging the back-off")
	}
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Errorf("the key waits %s after its sixth failure, want 2s", d)
	}
	if engaged, _ := th.Fail(bg, authn.ScopeIdentifier, "k"); engaged {
		t.Error("a later failure reported engaging a back-off that was already on")
	}
}

// TestTheBackoffDoublesAndEnds holds the curve: each failure beyond the free ones
// doubles the wait, the wait runs down with the clock, and it is capped.
func TestTheBackoffDoublesAndEnds(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, ThrottleParams{FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Minute})

	failN(t, th, "k", 1)
	for i, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second} {
		failN(t, th, "k", 1)
		if got := wait(t, th, "k"); got != want {
			t.Fatalf("after %d failures beyond the free one the key waits %s, want %s", i+1, got, want)
		}
	}
	failN(t, th, "k", 1)
	if got := wait(t, th, "k"); got != time.Minute {
		t.Errorf("past the cap the key waits %s, want the cap of a minute", got)
	}

	clock.advance(45 * time.Second)
	if got := wait(t, th, "k"); got != 15*time.Second {
		t.Errorf("after 45s of a minute the key waits %s, want 15s", got)
	}
	clock.advance(15 * time.Second)
	if got := wait(t, th, "k"); got != 0 {
		t.Errorf("when the wait has run out the key still waits %s", got)
	}
}

// TestTheDelayDoesNotOverflow checks the wait stays at the cap instead of
// wrapping negative or to zero.
func TestTheDelayDoesNotOverflow(t *testing.T) {
	clock := newClock()
	p := ThrottleParams{FreeAttempts: 1, Window: 24 * time.Hour, MaxBackoff: 24 * time.Hour}
	th := throttleAt(clock, p)

	failN(t, th, "k", 200)
	if got := wait(t, th, "k"); got != 24*time.Hour {
		t.Errorf("after 200 failures the key waits %s, want the cap of 24h", got)
	}
	for _, n := range []int{31, 32, 33, 34, 62, 63, 64, 65, 1000} {
		if d := p.delay(p.FreeAttempts + n); d != 24*time.Hour {
			t.Errorf("the delay %d failures past the free ones is %s, want the cap", n, d)
		}
	}
}

// TestTheCountReturnsToZeroAfterTheWindow checks the count resets at the
// window's edge and the next failure starts fresh.
func TestTheCountReturnsToZeroAfterTheWindow(t *testing.T) {
	params := ThrottleParams{FreeAttempts: 1, Window: time.Minute, MaxBackoff: time.Hour}

	t.Run("one nanosecond short, the count stands", func(t *testing.T) {
		clock := newClock()
		th := throttleAt(clock, params)
		failN(t, th, "k", 5)
		if d := wait(t, th, "k"); d != 16*time.Second {
			t.Fatalf("the key waits %s after five failures, want 16s", d)
		}
		clock.advance(time.Minute - time.Nanosecond)
		if d := wait(t, th, "k"); d != 0 {
			t.Fatalf("the key still waits %s after its wait ran out", d)
		}
		failN(t, th, "k", 1)
		if d := wait(t, th, "k"); d != 32*time.Second {
			t.Errorf("the key waits %s after a sixth failure, want 32s: the count was forgotten one nanosecond early", d)
		}
	})

	t.Run("at the window, the count is forgotten", func(t *testing.T) {
		clock := newClock()
		th := throttleAt(clock, params)
		failN(t, th, "k", 5)
		clock.advance(time.Minute)
		if engaged := failN(t, th, "k", 1); engaged != 0 {
			t.Error("a failure after the window was counted on top of the old count")
		}
		if d := wait(t, th, "k"); d != 0 {
			t.Errorf("the key waits %s after the first failure of a new count", d)
		}
		if engaged := failN(t, th, "k", 1); engaged != 1 {
			t.Error("the second failure of the new count did not engage the back-off")
		}
	})
}

// TestAWaitLongerThanTheWindowIsServedInFull checks a max-backoff above the
// window is served out, not cut short.
func TestAWaitLongerThanTheWindowIsServedInFull(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, ThrottleParams{FreeAttempts: 1, Window: time.Minute, MaxBackoff: time.Hour})

	failN(t, th, "k", 40)
	if d := wait(t, th, "k"); d != time.Hour {
		t.Fatalf("the key waits %s after forty failures, want the cap of 1h", d)
	}
	clock.advance(61 * time.Second)
	if d := wait(t, th, "k"); d != time.Hour-61*time.Second {
		t.Errorf("the key waits %s a window and a second later, want the rest of the hour", d)
	}
	// Other keys failing in the meantime sweep the table; the locked key stays.
	failN(t, th, "other", 1)
	if d := wait(t, th, "k"); d != time.Hour-61*time.Second {
		t.Errorf("the key waits %s after the table was swept, want the rest of the hour", d)
	}
	clock.advance(time.Hour - 61*time.Second - time.Nanosecond)
	if d := wait(t, th, "k"); d != time.Nanosecond {
		t.Errorf("the key waits %s one nanosecond before the end, want 1ns", d)
	}
	clock.advance(time.Nanosecond)
	if d := wait(t, th, "k"); d != 0 {
		t.Errorf("the key waits %s at the end of its wait, want none", d)
	}
	if engaged := failN(t, th, "k", 1); engaged != 0 {
		t.Error("a failure after the wait and the window was counted on top of the old count")
	}
}

// TestASuccessClearsTheKeyAndNoOtherKey holds Succeed.
func TestASuccessClearsTheKeyAndNoOtherKey(t *testing.T) {
	th := throttleAt(newClock(), DefaultThrottleParams())
	failN(t, th, "a", 8)
	failN(t, th, "b", 8)

	if err := th.Succeed(bg, authn.ScopeIdentifier, "a"); err != nil {
		t.Fatal(err)
	}
	if d := wait(t, th, "a"); d != 0 {
		t.Errorf("a key waits %s after a success", d)
	}
	if d := wait(t, th, "b"); d == 0 {
		t.Error("a success on one key freed another")
	}
	if engaged := failN(t, th, "a", 5); engaged != 0 {
		t.Error("the count did not start again from zero after a success")
	}
}

// TestKeysOfDifferentScopesNeverMeet checks the same string under two scopes is
// two keys.
func TestKeysOfDifferentScopesNeverMeet(t *testing.T) {
	th := throttleAt(newClock(), DefaultThrottleParams())
	for range 8 {
		if _, err := th.Fail(bg, authn.ScopeIdentifier, "k"); err != nil {
			t.Fatal(err)
		}
	}
	if d, _ := th.Check(bg, authn.ScopeAccount, "k"); d != 0 {
		t.Error("failures under one scope held back the same key under another")
	}
	if d, _ := th.Check(bg, authn.ScopeIdentifier, "k"); d == 0 {
		t.Error("failures under a scope did not hold the key back under it")
	}
	// The pairs a joiner built on a separator byte would confuse: the byte
	// itself in either part, and a boundary that moves with the same bytes.
	for _, c := range []struct{ a, b [2]string }{
		{[2]string{"a", "b\x00c"}, [2]string{"a\x00b", "c"}},
		{[2]string{"a", "bc"}, [2]string{"ab", "c"}},
		{[2]string{"1", "2:x"}, [2]string{"1:2", "x"}},
	} {
		if tableKey(authn.ThrottleScope(c.a[0]), c.a[1]) == tableKey(authn.ThrottleScope(c.b[0]), c.b[1]) {
			t.Errorf("the pairs %q and %q share one key", c.a, c.b)
		}
	}
}

// TestParametersAreReadWhenTheyAreUsed checks a changed setting applies to the
// next call and a value outside its domain reads as the default.
func TestParametersAreReadWhenTheyAreUsed(t *testing.T) {
	clock := newClock()
	p := ThrottleParams{FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Hour}
	th := NewThrottle(func() ThrottleParams { return p }, clock.now)

	failN(t, th, "k", 2)
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Fatalf("the key waits %s, want 2s", d)
	}
	p.FreeAttempts = 5
	if d := wait(t, th, "k"); d != 0 {
		t.Errorf("after the free attempts were raised the key waits %s, want none", d)
	}

	for name, bad := range map[string]ThrottleParams{
		"free attempts below the domain": {FreeAttempts: 0, Window: time.Hour, MaxBackoff: time.Hour},
		"free attempts above the domain": {FreeAttempts: 21, Window: time.Hour, MaxBackoff: time.Hour},
		"a window below the domain":      {FreeAttempts: 1, Window: time.Second, MaxBackoff: time.Hour},
		"a window above the domain":      {FreeAttempts: 1, Window: 48 * time.Hour, MaxBackoff: time.Hour},
		"a cap below the domain":         {FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Second},
		"a cap above the domain":         {FreeAttempts: 1, Window: time.Hour, MaxBackoff: 48 * time.Hour},
		"the zero value":                 {},
	} {
		got := bad.Normalized()
		d := DefaultThrottleParams()
		if got.FreeAttempts < MinFreeAttempts || got.FreeAttempts > MaxFreeAttempts ||
			got.Window < MinWindow || got.Window > MaxWindow ||
			got.MaxBackoff < MinBackoff || got.MaxBackoff > MaxBackoff {
			t.Errorf("%s: normalised to %+v, which is outside the domains", name, got)
		}
		if name == "the zero value" && got != d {
			t.Errorf("the zero value normalises to %+v, want the defaults %+v", got, d)
		}
	}
	in := ThrottleParams{FreeAttempts: 3, Window: 2 * time.Minute, MaxBackoff: time.Minute}
	if got := in.Normalized(); got != in {
		t.Errorf("parameters inside their domains were changed: %+v", got)
	}
}

// TestTheTableDoesNotGrowPastItsBound checks a full table forgets the key quiet
// longest, not one touched lately.
func TestTheTableDoesNotGrowPastItsBound(t *testing.T) {
	th := throttleAt(newClock(), ThrottleParams{FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Hour})

	failN(t, th, "first", 3)
	for i := 0; i < MaxThrottleKeys-1; i++ {
		failN(t, th, "k"+strconv.Itoa(i), 1)
	}
	if got := th.Len(); got != MaxThrottleKeys {
		t.Fatalf("the table holds %d keys, want %d", got, MaxThrottleKeys)
	}
	// Touch the first key, so that it is no longer the oldest.
	failN(t, th, "first", 1)
	failN(t, th, "one-too-many", 1)

	if got := th.Len(); got != MaxThrottleKeys {
		t.Errorf("the table holds %d keys after one more, want the bound of %d", got, MaxThrottleKeys)
	}
	if d := wait(t, th, "first"); d == 0 {
		t.Error("the key touched lately was the one forgotten")
	}
	failN(t, th, "k0", 1)
	if got := th.Len(); got != MaxThrottleKeys {
		t.Errorf("the table holds %d keys, want the bound", got)
	}
}

// TestConcurrentFailuresAreAllCounted checks every concurrent failure counts
// once and exactly one engages the back-off.
func TestConcurrentFailuresAreAllCounted(t *testing.T) {
	th := throttleAt(newClock(), ThrottleParams{FreeAttempts: 5, Window: time.Hour, MaxBackoff: time.Hour})
	const callers = 64

	var mu sync.Mutex
	engaged := 0
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e, _ := th.Fail(bg, authn.ScopeIdentifier, "k"); e {
				mu.Lock()
				engaged++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if engaged != 1 {
		t.Errorf("%d failures reported engaging the back-off, want exactly one", engaged)
	}
	// 64 failures with 5 free: 59 doublings, which is the cap.
	if d := wait(t, th, "k"); d != time.Hour {
		t.Errorf("the key waits %s after %d failures, want the cap of an hour: failures were lost", d, callers)
	}
}

// TestAFixedKeyIsNotPushedOutBySprayedNames checks a spray of names cannot free a
// fixed key or grow the table past its bound plus the fixed keys.
func TestAFixedKeyIsNotPushedOutBySprayedNames(t *testing.T) {
	p := ThrottleParams{FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Hour}
	fixed := Key{Scope: authn.ScopeIdentifier, Key: ThrottleKey("alice")}
	th := NewThrottle(func() ThrottleParams { return p }, newClock().now, fixed)
	th.limit = 64

	failN(t, th, fixed.Key, 4)
	held := wait(t, th, fixed.Key)
	if held == 0 {
		t.Fatal("four failures with one free attempt did not hold the fixed key back")
	}
	for i := range 10 * th.limit {
		failN(t, th, ThrottleKey("sprayed-"+strconv.Itoa(i)), 1)
		if got := th.Len(); got > th.limit+1 {
			t.Fatalf("after %d sprayed names the table holds %d keys, want at most %d", i+1, got, th.limit+1)
		}
	}
	if got := wait(t, th, fixed.Key); got != held {
		t.Errorf("the fixed key waits %s after the spray, want the %s it waited before: it was forgotten", got, held)
	}
	failN(t, th, "ordinary", 4)
	for i := range 10 * th.limit {
		failN(t, th, "spray-"+strconv.Itoa(i), 1)
	}
	if got := wait(t, th, "ordinary"); got != 0 {
		t.Errorf("an ordinary key still waits %s after a spray of ten times the table", got)
	}
}

// TestAFixedKeyBehavesLikeAnyOtherKey checks a fixed key takes the same waits
// and a success clears it without removing it.
func TestAFixedKeyBehavesLikeAnyOtherKey(t *testing.T) {
	p := ThrottleParams{FreeAttempts: 2, Window: time.Hour, MaxBackoff: time.Minute}
	clk := newClock()
	th := NewThrottle(func() ThrottleParams { return p }, clk.now, Key{Scope: authn.ScopeIdentifier, Key: "fixed"})

	for i := 1; i <= 12; i++ {
		failN(t, th, "fixed", 1)
		failN(t, th, "loose", 1)
		if a, b := wait(t, th, "fixed"), wait(t, th, "loose"); a != b {
			t.Fatalf("after %d failures the fixed key waits %s and the loose one %s", i, a, b)
		}
	}
	if wait(t, th, "fixed") != time.Minute {
		t.Errorf("the wait is %s, want the cap of a minute", wait(t, th, "fixed"))
	}

	if err := th.Succeed(bg, authn.ScopeIdentifier, "fixed"); err != nil {
		t.Fatal(err)
	}
	if err := th.Succeed(bg, authn.ScopeIdentifier, "loose"); err != nil {
		t.Fatal(err)
	}
	if wait(t, th, "fixed") != 0 || wait(t, th, "loose") != 0 {
		t.Error("a success did not clear the count")
	}
	if got := th.Len(); got != 1 {
		t.Errorf("the table holds %d keys after the successes, want the one fixed key", got)
	}
	if engaged := failN(t, th, "fixed", 3); engaged != 1 {
		t.Errorf("three failures of a cleared fixed key engaged the back-off %d times, want once", engaged)
	}
}

// TestKeysThatHaveGoneAWholeWindowAreSwept checks an unfixed key past its window
// is swept and one inside it is not.
func TestKeysThatHaveGoneAWholeWindowAreSwept(t *testing.T) {
	p := ThrottleParams{FreeAttempts: 1, Window: 10 * time.Minute, MaxBackoff: time.Hour}
	clk := newClock()
	th := NewThrottle(func() ThrottleParams { return p }, clk.now, Key{Scope: authn.ScopeIdentifier, Key: "fixed"})

	for i := range 100 {
		failN(t, th, "old-"+strconv.Itoa(i), 1)
	}
	failN(t, th, "fixed", 1)
	clk.advance(6 * time.Minute)
	failN(t, th, "recent", 1)
	if got := th.Len(); got != 102 {
		t.Fatalf("the table holds %d keys, want 100 old, one recent and the fixed one", got)
	}
	clk.advance(5 * time.Minute)
	wait(t, th, "anything")
	if got := th.Len(); got != 2 {
		t.Errorf("after the old keys' window the table holds %d keys, want the recent one and the fixed one", got)
	}
	failN(t, th, "recent", 1)
	if wait(t, th, "recent") == 0 {
		t.Error("a key inside its window was swept: its count was lost")
	}
}

// TestSpellingsThatDifferInCaseAndSpaceShareAKey checks case and white-space
// variants share one key and other names do not.
func TestSpellingsThatDifferInCaseAndSpaceShareAKey(t *testing.T) {
	want := ThrottleKey("alice")
	for _, spelling := range []string{"Alice", "ALICE", " alice", "alice\t", "a l i c e", " Alice\n"} {
		if got := ThrottleKey(spelling); got != want {
			t.Errorf("%q has a different key from %q", spelling, "alice")
		}
	}
	for _, other := range []string{"alicf", "alice1", "", "bob"} {
		if ThrottleKey(other) == want {
			t.Errorf("%q shares a key with %q", other, "alice")
		}
	}
	if len(want) != 64 {
		t.Errorf("the key is %d characters, want the 64 of a SHA-256 in hex", len(want))
	}
	if want == "alice" {
		t.Error("the key is the name itself and not a digest of it")
	}

	p := ThrottleParams{FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Hour}
	th := throttleAt(newClock(), p)
	failN(t, th, ThrottleKey("Alice"), 1)
	failN(t, th, ThrottleKey(" ALICE "), 1)
	if wait(t, th, ThrottleKey("alice")) == 0 {
		t.Error("failures under two spellings did not add up for the name")
	}
}
