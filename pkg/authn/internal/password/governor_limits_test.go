package password

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestAGovernorMadeWithNothingIsAGovernorOfOne: a Governor made with nothing is a Governor of one.
func TestAGovernorMadeWithNothingIsAGovernorOfOne(t *testing.T) {
	for _, g := range []*Governor{NewGovernor(0, 0), NewGovernor(-3, -3)} {
		first, err := g.acquire(context.Background(), 0, 0)
		if err != nil {
			t.Fatalf("the one slot of a Governor made with nothing is not there: %v", err)
		}
		_, err = g.acquire(context.Background(), 0, 0)
		if !errors.Is(err, authn.ErrBusy) {
			t.Errorf("a second slot of a Governor made with nothing gave %v, want one wrapping %v", err, authn.ErrBusy)
		}
		first()
		share, err := g.acquire(context.Background(), 1, 0)
		if err != nil {
			t.Fatalf("the one KiB of a Governor made with nothing is not there: %v", err)
		}
		share()
		_, err = g.acquire(context.Background(), 2, 0)
		if !errors.Is(err, authn.ErrBusy) {
			t.Errorf("two KiB of a Governor made with nothing gave %v, want one wrapping %v", err, authn.ErrBusy)
		}
	}
}

// TestAGovernorHashDrawsTheMemoryAnArgon2idDerivationTakes: the writing side draws the memory of an Argon2id derivation too.
func TestAGovernorHashDrawsTheMemoryAnArgon2idDerivationTakes(t *testing.T) {
	g := NewGovernor(4, 100)
	release, err := g.acquire(context.Background(), 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	big := Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}
	if _, err := g.Hash(context.Background(), big, "secret", 0); !errors.Is(err, authn.ErrBusy) {
		t.Errorf("writing an Argon2id entry with the memory taken gave %v, want one wrapping %v", err, authn.ErrBusy)
	}
	if _, err := g.Hash(context.Background(), cheapPBKDF2, "secret", 0); err != nil {
		t.Errorf("writing a PBKDF2 entry with the memory taken gave %v: it draws none", err)
	}
	release()
	if _, err := g.Hash(context.Background(), big, "secret", 0); err != nil {
		t.Errorf("writing an Argon2id entry after the memory was given back gave %v", err)
	}
}

// TestPBKDF2DrawsNoMemoryEvenWhenTheWholeBudgetIsHeld: PBKDF2 needs a slot and no share, even when the whole budget is held.
func TestPBKDF2DrawsNoMemoryEvenWhenTheWholeBudgetIsHeld(t *testing.T) {
	g := NewGovernor(2, 100)
	release, err := g.acquire(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	e := parsed(t, cheapPBKDF2, "secret")
	if e.MemoryKiB() != 0 {
		t.Errorf("a PBKDF2 entry draws %d KiB, want none", e.MemoryKiB())
	}
	if ok, err := g.Verify(context.Background(), e, "secret", 0); err != nil || !ok {
		t.Errorf("a PBKDF2 derivation with the whole budget held gave %v, %v: it draws none", ok, err)
	}
	if _, err := g.Hash(context.Background(), cheapPBKDF2, "secret", 0); err != nil {
		t.Errorf("writing a PBKDF2 entry with the whole budget held gave %v", err)
	}
}

// TestAnEndedContextIsAnsweredAsItselfWithNoWaitToo: a context that has ended is answered as itself with no wait, as with one.
func TestAnEndedContextIsAnsweredAsItselfWithNoWaitToo(t *testing.T) {
	g := NewGovernor(2, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, wait := range []time.Duration{0, time.Minute} {
		ok, err := g.Verify(ctx, e, "secret", wait)
		if !errors.Is(err, context.Canceled) || errors.Is(err, authn.ErrBusy) || ok {
			t.Errorf("a cancelled context with a wait of %s gave %v, %v, want the context's own error", wait, ok, err)
		}
		if _, err := g.Hash(ctx, cheapPBKDF2, "secret", wait); !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled context with a wait of %s gave %v when writing, want %v", wait, err, context.Canceled)
		}
	}
}

// TestACallersDeadlinePassingWhileItWaitsIsTheCallersAndNotTheWidthBeingFull: a deadline that passes while the caller waits for the width is the
// caller's, and the call has been waiting, so no early check can have answered.
func TestACallersDeadlinePassingWhileItWaitsIsTheCallersAndNotTheWidthBeingFull(t *testing.T) {
	g := NewGovernor(1, 1<<20)
	e := parsed(t, cheapPBKDF2, "secret")
	release, err := g.acquire(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	begun := time.Now()
	_, err = g.Verify(ctx, e, "secret", time.Minute)
	if elapsed := time.Since(begun); elapsed < 40*time.Millisecond {
		t.Fatalf("the call returned after %s, so it did not wait for the width", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, authn.ErrBusy) {
		t.Errorf("a deadline that passed while waiting gave %v, want the context's own error and not %v", err, authn.ErrBusy)
	}
}

// TestTheDefaultWidthIsTheProcessorsUpToEight: the width follows the processors up to eight and no more.
func TestTheDefaultWidthIsTheProcessorsUpToEight(t *testing.T) {
	prev := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prev)
	for procs, want := range map[int]int{1: 1, 2: 2, 5: 5, 7: 7, 8: 8, 9: 8, 64: 8} {
		runtime.GOMAXPROCS(procs)
		if got := DefaultConcurrency(); got != want {
			t.Errorf("with %d processors the default width is %d, want %d", procs, got, want)
		}
	}
}
