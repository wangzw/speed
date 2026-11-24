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

// TestWrongPasswordsThatArriveTogetherAreHeldAgainstOneAnother holds that simultaneous guesses get at most the free attempts and one more derivation.
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

// TestAGuessBetweenEveryPassIsStillCountedAndHeldBack holds that a pass between guesses clears nothing.
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

// TestAPassFromADerivationGivesBackItsOwnAttemptOnly holds that a derived pass returns its reserved attempt and no earlier failure.
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
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the right password after five failures reads %v (%v)", outcome(got), cause(got))
	}
	if derived.Load() != before+1 {
		t.Fatalf("the pass was not derived, so it was not the case under test")
	}
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("a second pass reads %v (%v): the first took more than its own attempt", outcome(got), cause(got))
	}
	b.Verify(context.Background(), basic("alice", "wrong"))
	if got := b.Verify(context.Background(), basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("after a pass and a sixth failure the right password reads %v (%v), want held back: the pass "+
			"cleared the failures before it", outcome(got), cause(got))
	}
}

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

// TestACachedPasswordIsNotAGuessAndHoldsNoAttempt holds that concurrent checks of a remembered password are all authenticated.
func TestACachedPasswordIsNotAGuessAndHoldsNoAttempt(t *testing.T) {
	const inflight = 12 // more than the free attempts and the one beyond them
	fs := newFakeSettings()
	fs.set(cacheTTLPath, time.Minute)
	store := &parkingStore{fakeSettings: fs, arrived: make(chan struct{}, inflight), release: make(chan struct{})}
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	derived := counting(b)

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

// TestARememberedPasswordIsRefusedWhileItsNameIsHeldBack holds that a cached check cannot bypass the back-off.
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

// TestAHotNameWithTheRightPasswordIsNeverHeldBackByItsOwnTraffic holds the property under real parallelism with no failure recorded.
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
