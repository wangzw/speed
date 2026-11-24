package password

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func standing(th *Throttle, key string) time.Duration {
	return th.Standing(authn.ScopeIdentifier, key)
}

// TestStandingCountsTheFailuresThatStandAndNotTheAttemptsInFlight holds the line
// between the two readings of a key: an attempt in flight is a failure for
// whoever is about to make an attempt of their own, and is not one for whoever
// only asks whether the key is being held back for what it has already done.
func TestStandingCountsTheFailuresThatStandAndNotTheAttemptsInFlight(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, defaultParams)
	failN(t, th, "k", 5)

	inflight := reserved(t, th, "k")
	if d := wait(t, th, "k"); d != 2*time.Second {
		t.Fatalf("a key with five failures and an attempt in flight waits %s, want 2s: the case under test "+
			"needs the attempt in flight to count for Check", d)
	}
	if d := standing(th, "k"); d != 0 {
		t.Errorf("a key with five failures and an attempt in flight stands at %s, want none: an attempt in "+
			"flight was counted as a failure that stands", d)
	}

	inflight.Fail()
	if d := standing(th, "k"); d != 2*time.Second {
		t.Errorf("a key whose sixth failure is recorded stands at %s, want 2s", d)
	}
	clock.advance(time.Second)
	if d := standing(th, "k"); d != time.Second {
		t.Errorf("a second later the key stands at %s, want 1s: the wait runs from the failure", d)
	}
	clock.advance(time.Second)
	if d := standing(th, "k"); d != 0 {
		t.Errorf("when the wait is over the key stands at %s, want none", d)
	}
}

// TestStandingAgesTheFailuresLikeEveryOtherReading holds that the failures a key
// stands on are the live ones: once the window and the wait they earned have
// passed, nothing stands.
func TestStandingAgesTheFailuresLikeEveryOtherReading(t *testing.T) {
	clock := newClock()
	th := throttleAt(clock, defaultParams)
	failN(t, th, "k", 9)
	if d := standing(th, "k"); d != 16*time.Second {
		t.Fatalf("nine failures stand at %s, want 16s", d)
	}
	clock.advance(15*time.Minute + time.Second)
	if d := standing(th, "k"); d != 0 {
		t.Errorf("after the window the failures stand at %s, want none", d)
	}
}

// TestStandingReservesNothingAndRemembersNothing holds that a reading changes no
// state: it admits no attempt, so the free attempts of the key are still the
// free attempts, and it does not make the table remember a key it was asked
// about.
func TestStandingReservesNothingAndRemembersNothing(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	before := th.Len()
	for range 100 {
		if d := standing(th, "unseen"); d != 0 {
			t.Fatalf("a key never seen stands at %s, want none", d)
		}
	}
	if th.Len() != before {
		t.Errorf("asking about a key never seen grew the table from %d to %d keys", before, th.Len())
	}

	failN(t, th, "k", 4)
	for range 100 {
		standing(th, "k")
	}
	if reserved(t, th, "k").Fail() {
		t.Error("the fifth failure engaged the back-off: readings were counted as attempts")
	}
	if !reserved(t, th, "k").Fail() {
		t.Error("the sixth failure did not engage the back-off")
	}
}

// TestStandingKeepsTheScopesApart holds that a key stands under the scope it
// failed in and under no other.
func TestStandingKeepsTheScopesApart(t *testing.T) {
	th := throttleAt(newClock(), defaultParams)
	failN(t, th, "k", 6)
	if d := th.Standing(authn.ScopeIdentifier, "k"); d == 0 {
		t.Fatalf("the key does not stand under the scope it failed in")
	}
	if d := th.Standing(authn.ScopeAccount, "k"); d != 0 {
		t.Errorf("the same key stands at %s under another scope, want none", d)
	}
}
