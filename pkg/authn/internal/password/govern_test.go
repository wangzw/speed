package password

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func parsed(t *testing.T, p Params, plaintext string) Entry {
	t.Helper()
	encoded, err := p.Hash(plaintext)
	if err != nil {
		t.Fatalf("writing an entry: %v", err)
	}
	e, err := Parse(encoded)
	if err != nil {
		t.Fatalf("reading an entry back: %v", err)
	}
	return e
}

var cheapPBKDF2 = Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1}

// TestADerivationThatFindsTheWidthTakenIsRefusedAsBusyAndDoesNotQueue holds the
// concurrency bound from both sides: with the width held a derivation is refused
// with ErrBusy at once and verifies nothing, and with the width released the
// same call goes through.
func TestADerivationThatFindsTheWidthTakenIsRefusedAsBusyAndDoesNotQueue(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")

	release, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("taking the only slot: %v", err)
	}

	ok, err := g.Verify(context.Background(), e, "secret", 0)
	if !errors.Is(err, authn.ErrBusy) {
		t.Fatalf("a derivation with the width taken gave %v, want one wrapping %v", err, authn.ErrBusy)
	}
	if ok {
		t.Error("a derivation that was refused as busy reported a match")
	}

	release()
	ok, err = g.Verify(context.Background(), e, "secret", 0)
	if err != nil || !ok {
		t.Errorf("a derivation after the width was released gave %v, %v, want a match", ok, err)
	}
}

// TestTheSlotIsGivenBackWhateverTheDerivationDid holds that a finished
// derivation, a failed one and a refused one all leave the width as it was.
func TestTheSlotIsGivenBackWhateverTheDerivationDid(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")

	for i := 0; i < 3; i++ {
		if ok, err := g.Verify(context.Background(), e, "wrong", 0); err != nil || ok {
			t.Fatalf("round %d: a wrong password gave %v, %v", i, ok, err)
		}
		if ok, err := g.Verify(context.Background(), e, "secret", 0); err != nil || !ok {
			t.Fatalf("round %d: the right password gave %v, %v", i, ok, err)
		}
	}
	if _, err := g.Verify(context.Background(), Entry{}, "x", 0); err == nil {
		t.Fatal("the zero entry verified")
	}
	if ok, err := g.Verify(context.Background(), e, "secret", 0); err != nil || !ok {
		t.Errorf("after a derivation against the zero entry the width is not free: %v, %v", ok, err)
	}
}

// TestTheMemoryBudgetBoundsWhatArgon2idHoldsAtOnce holds the byte bound: an
// Argon2id derivation draws the memory its entry states, one that would pass the
// budget is refused, PBKDF2 draws none, and the share comes back.
func TestTheMemoryBudgetBoundsWhatArgon2idHoldsAtOnce(t *testing.T) {
	g := NewGovernor(8, 100)
	big := parsed(t, Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}, "secret")
	pbkdf := parsed(t, cheapPBKDF2, "secret")

	release, err := g.acquire(context.Background(), big.MemoryKiB(), 0)
	if err != nil {
		t.Fatalf("taking 64 KiB of 100: %v", err)
	}
	if _, err := g.Verify(context.Background(), big, "secret", 0); !errors.Is(err, authn.ErrBusy) {
		t.Errorf("a second 64 KiB against a budget of 100 gave %v, want one wrapping %v", err, authn.ErrBusy)
	}
	if ok, err := g.Verify(context.Background(), pbkdf, "secret", 0); err != nil || !ok {
		t.Errorf("a PBKDF2 derivation with the memory taken gave %v, %v: it draws none", ok, err)
	}
	release()
	if ok, err := g.Verify(context.Background(), big, "secret", 0); err != nil || !ok {
		t.Errorf("a derivation after the memory was given back gave %v, %v", ok, err)
	}
}

// TestAShareThatCanNeverBeHadIsRefusedAtOnce holds that an entry asking for more
// than the whole budget does not wait for it, whatever wait the caller allows.
func TestAShareThatCanNeverBeHadIsRefusedAtOnce(t *testing.T) {
	g := NewGovernor(2, 32)
	e := parsed(t, Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}, "secret")

	begun := time.Now()
	_, err := g.Verify(context.Background(), e, "secret", time.Minute)
	if !errors.Is(err, authn.ErrBusy) {
		t.Fatalf("a share larger than the budget gave %v, want one wrapping %v", err, authn.ErrBusy)
	}
	if time.Since(begun) > 10*time.Second {
		t.Error("a share that can never be had waited for it")
	}
}

// TestAWaitIsHonouredBeforeABusyAnswer holds the wait a caller allows: a
// derivation that finds the width taken waits at least that long before it is
// refused, and is refused as busy and not as the caller's context.
func TestAWaitIsHonouredBeforeABusyAnswer(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")
	release, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	const wait = 50 * time.Millisecond
	begun := time.Now()
	_, err = g.Verify(context.Background(), e, "secret", wait)
	if elapsed := time.Since(begun); elapsed < wait {
		t.Errorf("the derivation was refused after %s, before the %s it was allowed to wait", elapsed, wait)
	}
	if !errors.Is(err, authn.ErrBusy) {
		t.Errorf("the derivation gave %v, want one wrapping %v", err, authn.ErrBusy)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Errorf("the derivation reports the caller's context for what was the width being taken: %v", err)
	}
}

// TestAWaitingDerivationGetsTheSlotWhenItIsReleased holds that waiting is not
// only delaying a refusal: a derivation that waits goes through once the width
// is free.
func TestAWaitingDerivationGetsTheSlotWhenItIsReleased(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")
	release, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, err := g.Verify(context.Background(), e, "secret", time.Minute)
		done <- result{ok, err}
	}()
	release()
	got := <-done
	if got.err != nil || !got.ok {
		t.Errorf("the waiting derivation gave %v, %v after the width was released", got.ok, got.err)
	}
}

// TestACallersOwnContextIsNotTheWidthBeingFull holds the distinction the
// backend's answer depends on: a context that has ended is reported as itself,
// before any capacity is taken, and not as ErrBusy.
func TestACallersOwnContextIsNotTheWidthBeingFull(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ok, err := g.Verify(ctx, e, "secret", time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context gave %v, want one wrapping %v", err, context.Canceled)
	}
	if errors.Is(err, authn.ErrBusy) || ok {
		t.Errorf("a cancelled context was reported as busy (%v) or as a match (%v)", err, ok)
	}

	release, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// The slot is held and the wait is a minute, so the only way out is the
	// cancellation; it comes from a timer and not from a goroutine racing the
	// call, and the call is made on this goroutine.
	waiting, stop := context.WithCancel(context.Background())
	defer stop()
	time.AfterFunc(20*time.Millisecond, stop)
	if _, err := g.Verify(waiting, e, "secret", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("a context cancelled while waiting gave %v, want one wrapping %v", err, context.Canceled)
	}
}

// TestNoMoreThanTheWidthRunsAtOnce holds the bound under load: with a width
// of two and many callers, the derivations inside the Governor never exceed
// two.
func TestNoMoreThanTheWidthRunsAtOnce(t *testing.T) {
	const width, callers = 2, 24
	g := NewGovernor(width, 1<<20)

	var mu sync.Mutex
	running, peak := 0, 0
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := g.acquire(context.Background(), 0, time.Minute)
			if err != nil {
				t.Errorf("a caller allowed a minute was refused: %v", err)
				return
			}
			defer release()
			mu.Lock()
			running++
			peak = max(peak, running)
			mu.Unlock()
			// Hold the section until two have arrived or the others have
			// all finished, so that the peak is reached when it can be.
			time.Sleep(time.Millisecond)
			mu.Lock()
			running--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if peak > width {
		t.Errorf("%d derivations ran at once with a width of %d", peak, width)
	}
	if peak < 1 {
		t.Error("no derivation ran")
	}
}

// TestTheDefaultsAreWhatTheDesignStates holds the two defaults a Governor is
// built with when nothing says otherwise.
func TestTheDefaultsAreWhatTheDesignStates(t *testing.T) {
	if DefaultMemoryBudgetKiB != 512*1024 {
		t.Errorf("the default budget is %d KiB, want 512 MiB", DefaultMemoryBudgetKiB)
	}
	if got := DefaultConcurrency(); got < 1 || got > 8 {
		t.Errorf("the default width is %d, want one to eight", got)
	}
	if MaxMemoryKiB*2 > DefaultMemoryBudgetKiB {
		t.Errorf("one entry may hold %d KiB, which is more than half the default budget of %d KiB",
			MaxMemoryKiB, DefaultMemoryBudgetKiB)
	}
}

// TestAGovernorHashesUnderTheSameBounds holds the writing side of the Governor:
// it writes a readable entry when it has capacity and is refused as busy when it
// has none, and a parameter set no reader would accept is refused before any
// capacity is taken.
func TestAGovernorHashesUnderTheSameBounds(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	encoded, err := g.Hash(context.Background(), cheapPBKDF2, "secret", 0)
	if err != nil {
		t.Fatalf("writing an entry: %v", err)
	}
	if _, parseErr := Parse(encoded); parseErr != nil {
		t.Errorf("the entry the Governor wrote is not readable: %v", parseErr)
	}

	release, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Hash(context.Background(), cheapPBKDF2, "secret", 0); !errors.Is(err, authn.ErrBusy) {
		t.Errorf("writing with the width taken gave %v, want one wrapping %v", err, authn.ErrBusy)
	}
	if _, err := g.Hash(context.Background(), Params{}, "secret", 0); !errors.Is(err, ErrMalformedHash) {
		t.Errorf("writing with no algorithm gave %v, want one wrapping %v", err, ErrMalformedHash)
	}
	release()
}

// TestTheDefaultWaitIsASecond holds the wait an interactive caller gives a
// derivation that finds capacity taken.
func TestTheDefaultWaitIsASecond(t *testing.T) {
	if DefaultWait != time.Second {
		t.Errorf("the default wait is %s, want one second", DefaultWait)
	}
}

// TestAMemoryShareThatIsNotFreedWithinTheWaitIsBusyAndGivesTheSlotBack holds the
// budget as a bound apart from the width: with the slots free and the memory
// taken, an Argon2id derivation waits the time it was allowed, is refused as
// busy and not as the caller's context, and does not keep the slot it took on
// the way; a derivation that does not draw memory goes through meanwhile.
func TestAMemoryShareThatIsNotFreedWithinTheWaitIsBusyAndGivesTheSlotBack(t *testing.T) {
	g := NewGovernor(2, 100)
	big := parsed(t, Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}, "secret")
	release, err := g.acquire(context.Background(), 64, 0)
	if err != nil {
		t.Fatal(err)
	}

	const wait = 50 * time.Millisecond
	begun := time.Now()
	_, err = g.Verify(context.Background(), big, "secret", wait)
	if elapsed := time.Since(begun); elapsed < wait {
		t.Errorf("the derivation was refused after %s, before the %s it was allowed to wait", elapsed, wait)
	}
	if !errors.Is(err, authn.ErrBusy) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a derivation that found the memory taken gave %v, want one wrapping %v alone", err, authn.ErrBusy)
	}
	if ok, err := g.Verify(context.Background(), parsed(t, cheapPBKDF2, "secret"), "secret", 0); err != nil || !ok {
		t.Errorf("after a refused wait for memory the slot is still held: %v, %v", ok, err)
	}
	release()
	if ok, err := g.Verify(context.Background(), big, "secret", 0); err != nil || !ok {
		t.Errorf("after the memory was given back the derivation gave %v, %v", ok, err)
	}
}

// TestAShareIsGivenBackWhenTheCallerLeavesWhileWaitingForIt holds that a caller
// whose context ends while it waits for memory leaves neither the slot nor the
// memory taken.
func TestAShareIsGivenBackWhenTheCallerLeavesWhileWaitingForIt(t *testing.T) {
	g := NewGovernor(2, 100)
	big := parsed(t, Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}, "secret")
	release, err := g.acquire(context.Background(), 64, 0)
	if err != nil {
		t.Fatal(err)
	}

	ctx, leave := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, verr := g.Verify(ctx, big, "secret", time.Minute)
		result <- verr
	}()
	leave()
	if got := <-result; !errors.Is(got, context.Canceled) {
		t.Fatalf("a caller that left gave %v, want one wrapping %v", got, context.Canceled)
	}
	release()
	first, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Errorf("a slot is still held after the caller left: %v", err)
		return
	}
	defer first()
	again, err := g.acquire(context.Background(), 100, 0)
	if err != nil {
		t.Errorf("the budget and the slot are not all free after the caller left: %v", err)
		return
	}
	again()
}
