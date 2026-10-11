package static

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/settings"
)

// TestWrongPasswordsThatArriveTogetherAreHeldAgainstOneAnother holds the free
// attempts of a name to the free attempts and not to the width: with every
// derivation held open, no more than the five free attempts and the one beyond
// them get as far as a derivation, however many arrive together, and the rest
// are refused as held back without one.
func TestWrongPasswordsThatArriveTogetherAreHeldAgainstOneAnother(t *testing.T) {
	const arriving = 64
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)

	var started atomic.Int64
	release := make(chan struct{})
	b.check = func(context.Context, password.Entry, string, time.Duration) (bool, error) {
		started.Add(1)
		<-release
		return false, nil
	}

	verdicts := make(chan authn.Verdict, arriving)
	var wg sync.WaitGroup
	for range arriving {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verdicts <- b.Verify(context.Background(), basic("alice", "wrong"))
		}()
	}

	// The refused ones answer while the derivations are still held open.
	var refused int
	timeout := time.After(10 * time.Second)
	for refused < arriving-6 {
		select {
		case got := <-verdicts:
			if !errors.Is(cause(got), authn.ErrThrottled) {
				t.Errorf("an attempt answered while every derivation was held open reads %v (%v), want held back",
					outcome(got), cause(got))
			}
			refused++
		case <-timeout:
			close(release)
			wg.Wait()
			t.Fatalf("only %d of %d attempts were refused, and %d derivations began: attempts that arrived "+
				"together were not held against one another", refused, arriving-6, started.Load())
		}
	}
	close(release)
	wg.Wait()
	if got := started.Load(); got != 6 {
		t.Errorf("%d derivations began for %d wrong passwords that arrived together, want 6: the five free "+
			"attempts and the one beyond them", got, arriving)
	}
}

// TestAGuessBetweenEveryPassIsStillCountedAndHeldBack holds that a pass clears
// nothing: a caller who presents the right password between every two guesses
// is held back after the same six derivations as one who does not.
func TestAGuessBetweenEveryPassIsStillCountedAndHeldBack(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	derived := counting(b)

	var guessesDerived int
	for i := range 40 {
		before := derived.Load()
		got := b.Verify(context.Background(), basic("alice", "guess"))
		if derived.Load() > before {
			guessesDerived++
		} else if !errors.Is(cause(got), authn.ErrThrottled) {
			t.Fatalf("guess %d was neither derived nor held back: %v (%v)", i+1, outcome(got), cause(got))
		}
		b.Verify(context.Background(), basic("alice", "secret"))
	}
	if guessesDerived != 6 {
		t.Errorf("%d guesses were derived with a pass between each of them, want 6", guessesDerived)
	}
}

// TestAPassFromADerivationGivesBackItsOwnAttemptOnly holds the same two edges
// for a pass that was derived, with the cache off: the attempt it reserved is
// given back and the failures before it are not.
func TestAPassFromADerivationGivesBackItsOwnAttemptOnly(t *testing.T) {
	store := newFakeSettings()
	store.set(cacheTTLPath, time.Duration(0))
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	derived := counting(b)

	for range 5 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	before := derived.Load()
	// Five failures stand, so the sixth attempt is the last one made, and it
	// passes: its own reservation goes back and the five stay.
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the right password after five failures reads %v (%v)", outcome(got), cause(got))
	}
	if derived.Load() != before+1 {
		t.Fatalf("the pass was not derived, so it was not the case under test")
	}
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("a second pass reads %v (%v): the first took more than its own attempt", outcome(got), cause(got))
	}
	// Five stand: one more failure is the sixth and holds the name back.
	b.Verify(context.Background(), basic("alice", "wrong"))
	if got := b.Verify(context.Background(), basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("after a pass and a sixth failure the right password reads %v (%v), want held back: the pass "+
			"cleared the failures before it", outcome(got), cause(got))
	}
}

// parkingStore is a settings store that holds every read of the cache lifetime
// until the case lets it go, and says each time a read has arrived.
type parkingStore struct {
	*fakeSettings
	arrived chan struct{}
	release chan struct{}
}

func (s *parkingStore) Get(l settings.Layer, key string, target any) error {
	if key == cacheTTLPath {
		s.arrived <- struct{}{}
		<-s.release
	}
	return s.fakeSettings.Get(l, key, target)
}

// TestACachedPasswordIsNotAGuessAndHoldsNoAttempt holds that a password that is
// remembered from a derivation takes no part in the free attempts of its name:
// with more calls for the right password in flight at once than a name has free
// attempts, every one of them, and the one that arrives after them, is
// authenticated. A call is held at the read of the cache lifetime, the first
// thing a cached check does that is not the throttle's own, so one that held an
// attempt from the moment it was admitted would still hold it there.
func TestACachedPasswordIsNotAGuessAndHoldsNoAttempt(t *testing.T) {
	const inflight = 12 // more than the free attempts and the one beyond them
	fs := newFakeSettings()
	fs.set(cacheTTLPath, time.Minute)
	store := &parkingStore{fakeSettings: fs, arrived: make(chan struct{}, inflight), release: make(chan struct{})}
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	derived := counting(b)

	// Remember the password with the store open.
	close(store.release)
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("remembering the password: %v (%v)", outcome(got), cause(got))
	}
	<-store.arrived
	store.release = make(chan struct{})
	remembered := derived.Load()

	verdicts := make(chan authn.Verdict, inflight)
	for range inflight {
		go func() { verdicts <- b.Verify(context.Background(), basic("alice", "secret")) }()
	}
	// Every call is at the read, or has already been answered: one that took an
	// attempt would have been refused before it got there once the free attempts
	// were taken.
	parked, answered := 0, 0
	timeout := time.After(10 * time.Second)
	for parked+answered < inflight {
		select {
		case <-store.arrived:
			parked++
		case got := <-verdicts:
			answered++
			t.Errorf("a call for the remembered password was answered while the others were held, as %v (%v)",
				outcome(got), cause(got))
		case <-timeout:
			close(store.release)
			t.Fatalf("%d of %d calls reached the read of the cache lifetime", parked, inflight)
		}
	}

	// One more call, with all of them in flight, is a cached password too.
	late := make(chan authn.Verdict, 1)
	go func() { late <- b.Verify(context.Background(), basic("alice", "secret")) }()
	var got authn.Verdict
	select {
	case <-store.arrived:
		close(store.release)
		got = <-late
	case got = <-late:
		close(store.release)
	case <-timeout:
		close(store.release)
		t.Fatalf("the call that arrived last was neither answered nor held at the read")
	}
	if outcome(got) != verdict.Authenticated {
		t.Errorf("a remembered password that arrived while %d others were in flight reads %v (%v), want "+
			"authenticated: a check that is not a guess held an attempt", inflight, outcome(got), cause(got))
	}
	for range inflight - answered {
		if got := <-verdicts; outcome(got) != verdict.Authenticated {
			t.Errorf("a remembered password that was in flight reads %v (%v), want authenticated",
				outcome(got), cause(got))
		}
	}
	if derived.Load() != remembered {
		t.Errorf("%d derivations ran for passwords that were remembered", derived.Load()-remembered)
	}
}

// TestARememberedPasswordIsRefusedWhileItsNameIsHeldBack holds that a cached
// check is not a way around the back-off: a name whose failures stand and have
// earned a wait is refused with the right password too, without a derivation,
// and is let in again when the wait is over.
func TestARememberedPasswordIsRefusedWhileItsNameIsHeldBack(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	clk := newClock()
	b.throttle = password.NewThrottle(b.throttleParams, clk.now)
	b.cache = password.NewCache(16, clk.now)
	derived := counting(b)

	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("remembering the password: %v (%v)", outcome(got), cause(got))
	}
	for range 6 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	before := derived.Load()

	got := b.Verify(context.Background(), basic("alice", "secret"))
	var held *authn.ThrottledError
	if !errors.As(cause(got), &held) || held.RetryAfter <= 0 {
		t.Fatalf("the remembered password of a name that is held back reads %v (%v), want it held back "+
			"with a wait", outcome(got), cause(got))
	}
	if derived.Load() != before {
		t.Errorf("a name that is held back was derived against")
	}

	clk.advance(held.RetryAfter)
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Errorf("the remembered password when the wait is over reads %v (%v), want authenticated",
			outcome(got), cause(got))
	}
}

// TestAHotNameWithTheRightPasswordIsNeverHeldBackByItsOwnTraffic holds the same
// property under real parallelism: workers that present the right password of
// one name over and over, with no failure ever recorded, are all authenticated,
// however the calls interleave.
func TestAHotNameWithTheRightPasswordIsNeverHeldBackByItsOwnTraffic(t *testing.T) {
	const workers, calls = 16, 1000
	b := tableWith(t, map[string]string{"alice": "secret"})
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("remembering the password: %v (%v)", outcome(got), cause(got))
	}

	var refused atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range calls {
				if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
					refused.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if refused.Load() != 0 {
		t.Errorf("%d of %d calls for the right password of a name that never failed were refused",
			refused.Load(), workers*calls)
	}
}
