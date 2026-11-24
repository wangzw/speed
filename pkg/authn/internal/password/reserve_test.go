package password

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

var defaultParams = ThrottleParams{FreeAttempts: 5, Window: 15 * time.Minute, MaxBackoff: 15 * time.Minute}

func reserved(t *testing.T, th *Throttle, key string) *Attempt {
	t.Helper()
	w, a := th.Reserve(authn.ScopeIdentifier, key)
	if w != 0 || a == nil {
		t.Fatalf("an attempt of %q is held back for %s, want it admitted", key, w)
	}
	return a
}

// TestAttemptsThatArriveTogetherAreAdmittedNoMoreThanTheFreeOnesAndOne admits the
// free attempts and one more under concurrent reservation.
func TestAttemptsThatArriveTogetherAreAdmittedNoMoreThanTheFreeOnesAndOne(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	const arriving = 64

	var mu sync.Mutex
	var admitted, held int
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range arriving {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w, a := th.Reserve(authn.ScopeIdentifier, "k")
			mu.Lock()
			defer mu.Unlock()
			if a != nil && w == 0 {
				admitted++
			} else if a == nil && w > 0 {
				held++
			}
		}()
	}
	close(start)
	wg.Wait()
	if admitted != 6 || held != arriving-6 {
		t.Errorf("%d attempts admitted and %d held back of %d, want 6 and %d", admitted, held, arriving, arriving-6)
	}
}

// TestAnAttemptSettledAsAPassGivesBackItsOwnReservationAndNothingElse refunds the
// attempt alone and keeps the failures before it.
func TestAnAttemptSettledAsAPassGivesBackItsOwnReservationAndNothingElse(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	failN(t, th, "k", 5)

	a := reserved(t, th, "k")
	if w, other := th.Reserve(authn.ScopeIdentifier, "k"); other != nil || w != 2*time.Second {
		t.Fatalf("an attempt that arrives while the sixth is in flight gets %s and %v, want 2s and no attempt", w, other)
	}
	a.Refund()

	if d := wait(t, th, "k"); d != 0 {
		t.Fatalf("the key waits %s after its attempt was given back, want none", d)
	}
	b := reserved(t, th, "k")
	if !b.Fail() {
		t.Error("the failure after a pass did not engage the back-off: the pass took more than its own attempt")
	}
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Errorf("the key waits %s after that failure, want 2s", d)
	}
}

// TestAPassDoesNotRestartTheAgingOfTheFailuresBeforeIt leaves the aging of earlier
// failures where a refund found it.
func TestAPassDoesNotRestartTheAgingOfTheFailuresBeforeIt(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, defaultParams)
	failN(t, th, "k", 3)

	clock.advance(14 * time.Minute)
	reserved(t, th, "k").Refund()
	clock.advance(90 * time.Second)

	for i := 1; i <= 5; i++ {
		if reserved(t, th, "k").Fail() {
			t.Fatalf("failure %d engaged the back-off: the pass restarted the aging of the failures before it", i)
		}
	}
	if !reserved(t, th, "k").Fail() {
		t.Error("the sixth failure after the count aged away did not engage the back-off")
	}
}

// TestTheWaitRunsFromTheAttemptInFlightAndThenFromTheFailure times the wait from
// the latest attempt in flight, then from the recorded failure.
func TestTheWaitRunsFromTheAttemptInFlightAndThenFromTheFailure(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, defaultParams)
	failN(t, th, "k", 5)

	clock.advance(time.Hour)
	var open []*Attempt
	for range 6 {
		open = append(open, reserved(t, th, "k"))
	}
	clock.advance(500 * time.Millisecond)
	if d := wait(t, th, "k"); d != 1500*time.Millisecond {
		t.Errorf("the key waits %s while six attempts are in flight, want 1.5s: the 2s of six failures from the latest attempt", d)
	}
	for _, a := range open[:5] {
		a.Fail()
	}
	if open[5].Fail() == false {
		t.Error("the failure that made the sixth did not engage the back-off")
	}
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Errorf("the key waits %s once every attempt failed, want 2s from the failure", d)
	}
}

// TestAnAttemptIsSettledOnce holds that a second settlement does nothing.
func TestAnAttemptIsSettledOnce(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	a := reserved(t, th, "k")
	a.Refund()
	a.Refund()
	if a.Fail() {
		t.Error("an attempt that was given back engaged the back-off when settled again")
	}
	failN(t, th, "k", 5)
	if d := wait(t, th, "k"); d != 0 {
		t.Errorf("the key waits %s after five failures, want none: the second settlement counted", d)
	}

	b := reserved(t, th, "k")
	b.Fail()
	b.Fail()
	b.Refund()
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Errorf("the key waits %s after six failures and settlements repeated, want 2s", d)
	}
}

// TestAKeyThatIsNotFixedIsForgottenWhenItsOnlyAttemptIsGivenBack forgets an
// unfixed key once its only attempt is refunded and keeps a fixed one.
func TestAKeyThatIsNotFixedIsForgottenWhenItsOnlyAttemptIsGivenBack(t *testing.T) {
	fixed := Key{Scope: authn.ScopeIdentifier, Key: "fixed"}
	th := NewThrottle(func() ThrottleParams { return defaultParams }, newClock().now, fixed)

	a := reserved(t, th, "loose")
	if got := th.Len(); got != 2 {
		t.Fatalf("the table holds %d keys with one attempt in flight, want 2", got)
	}
	a.Refund()
	if got := th.Len(); got != 1 {
		t.Errorf("the table holds %d keys after the attempt was given back, want only the fixed one", got)
	}
	reserved(t, th, "fixed").Refund()
	if got := th.Len(); got != 1 {
		t.Errorf("the table holds %d keys after a fixed key's attempt was given back, want the fixed one kept", got)
	}
}

// TestAKeyWithAnAttemptInFlightIsNotSweptAndASpentOneIs holds that the sweep
// leaves a key whose attempt has not settled, and reclaims it once it has.
func TestAKeyWithAnAttemptInFlightIsNotSweptAndASpentOneIs(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, defaultParams)
	a := reserved(t, th, "slow")

	clock.advance(24 * time.Hour)
	failN(t, th, "other", 1)
	if got := th.Len(); got != 2 {
		t.Fatalf("the table holds %d keys, want 2: a key with an attempt in flight was swept", got)
	}
	a.Fail()
	clock.advance(24 * time.Hour)
	failN(t, th, "other", 1)
	if got := th.Len(); got != 1 {
		t.Errorf("the table holds %d keys, want 1: the key whose failure aged away was kept", got)
	}
}

// TestAnAttemptWhoseKeyWasPushedOutStillCountsItsFailure records the failure of a
// dropped reservation under the key again.
func TestAnAttemptWhoseKeyWasPushedOutStillCountsItsFailure(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	failed := reserved(t, th, "pushed")
	given := reserved(t, th, "given")
	for i := range 16384 {
		failN(t, th, "spray"+strconv.Itoa(i), 1)
	}
	if got := th.Len(); got != 16384 {
		t.Fatalf("the table holds %d keys, want 16384", got)
	}
	given.Refund()
	if got := th.Len(); got != 16384 {
		t.Errorf("giving back an attempt whose key was pushed out changed the table to %d keys", got)
	}
	failed.Fail()
	failN(t, th, "pushed", 4)
	if d := wait(t, th, "pushed"); d != 0 {
		t.Fatalf("the key waits %s after five failures, want none", d)
	}
	if !reserved(t, th, "pushed").Fail() {
		t.Error("the sixth failure of the key did not engage the back-off: the failure of the attempt that was pushed out was lost")
	}
}

// TestSucceedLeavesAnAttemptInFlightCounting clears the earlier failures and keeps
// the attempt still in flight.
func TestSucceedLeavesAnAttemptInFlightCounting(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	failN(t, th, "k", 5)
	a := reserved(t, th, "k")
	if err := th.Succeed(bg, authn.ScopeIdentifier, "k"); err != nil {
		t.Fatal(err)
	}
	if got := th.Len(); got != 1 {
		t.Fatalf("the table holds %d keys, want the key with an attempt in flight kept", got)
	}
	failed := a.Fail()
	if failed {
		t.Error("the failure engaged the back-off with the five before it cleared")
	}
	if d := wait(t, th, "k"); d != 0 {
		t.Errorf("the key waits %s after one failure following a clear, want none", d)
	}
}

// TestTheTableBoundIsTheNumberTheDesignStates holds the back-off table at 16384
// keys.
func TestTheTableBoundIsTheNumberTheDesignStates(t *testing.T) {
	th := throttleAt(newClock(), ThrottleParams{FreeAttempts: 1, Window: time.Hour, MaxBackoff: time.Hour})
	for i := range 16384 {
		failN(t, th, "k"+strconv.Itoa(i), 2)
	}
	if got := th.Len(); got != 16384 {
		t.Fatalf("the table holds %d keys after 16384 distinct names, want all of them", got)
	}
	failN(t, th, "one-more", 2)
	if got := th.Len(); got != 16384 {
		t.Errorf("the table holds %d keys after 16385 distinct names, want it held at 16384", got)
	}
	if d := wait(t, th, "k0"); d != 0 {
		t.Errorf("the quietest key still waits %s when the table was full, want it forgotten", d)
	}
	for _, key := range []string{"k1", "one-more"} {
		if d := wait(t, th, key); d == 0 {
			t.Errorf("%q was forgotten, want it kept", key)
		}
	}
	if MaxThrottleKeys != 16384 {
		t.Errorf("MaxThrottleKeys is %d, want 16384", MaxThrottleKeys)
	}
}

// TestGivingBackAnAttemptWhoseKeyWasPushedOutLeavesTheKeysNewState keeps failures
// recorded under the key after its old reservation was dropped.
func TestGivingBackAnAttemptWhoseKeyWasPushedOutLeavesTheKeysNewState(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	dropped := reserved(t, th, "k")
	for i := range 16384 {
		failN(t, th, "spray"+strconv.Itoa(i), 1)
	}
	failN(t, th, "k", 6)
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Fatalf("the key waits %s after six failures, want 2s", d)
	}
	dropped.Refund()
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Errorf("the key waits %s after the dropped attempt was given back, want the 2s that stand", d)
	}
}

// TestAReservationAloneReclaimsKeysThatWentAWholeWindow reclaims an aged key on
// the next reservation for any key.
func TestAReservationAloneReclaimsKeysThatWentAWholeWindow(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, defaultParams)
	failN(t, th, "old", 1)
	clock.advance(defaultParams.Window)
	reserved(t, th, "new")
	if got := th.Len(); got != 1 {
		t.Errorf("the table holds %d keys after a reservation a whole window later, want 1: the old key was kept", got)
	}
}
