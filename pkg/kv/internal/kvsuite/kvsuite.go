// Package kvsuite holds the contract every implementation of pkg/kv must
// satisfy.
//
// It is written once and run by each backend, so "an implementation meets the
// capability's semantics" is a single statement a backend either passes or does
// not, rather than a claim each subpackage's own tests happen to cover. An
// implementation joins the contract by calling AssertConforms with a factory
// that builds an empty store.
//
// What is deliberately not here: durability, sharing across replicas, and
// behaviour after a restart. Those are properties of a backend rather than of
// the capability — an implementation that keeps values in one process's memory
// is not a failing implementation of this contract, it is one whose deployment
// precondition says so. A backend that does promise them tests them where its
// own machinery lives.
package kvsuite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/kv"
)

// Factory builds a fresh, empty store for one subtest. Each call returns a
// store with no relation to the ones before it.
type Factory func(t *testing.T) kv.KV

// ttlSettle is how long a case waits before asserting that a key with a ttl has
// aged out. It is comfortably longer than the ttls the cases set, and the
// direction of the assertion is what makes that safe: a backend that expires
// early still passes, one that never expires does not.
const ttlSettle = 150 * time.Millisecond

// shortTTL is the expiry the cases attach when they want one to pass.
const shortTTL = 30 * time.Millisecond

// liveTTL is the expiry the cases attach when they want a key to stay. It
// outlives ttlSettle by a wide margin, so a wait shorter than it proves nothing
// about a backend that expires too eagerly.
const liveTTL = time.Hour

// AssertConforms runs the whole contract against stores the factory builds.
func AssertConforms(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("absent key is reported, not failed", func(t *testing.T) {
		s := newStore(t)
		value, present, err := s.Get(context.Background(), "missing")
		if err != nil {
			t.Fatalf("Get on a missing key: %v", err)
		}
		if present {
			t.Fatalf("Get on a missing key reported present, with value %q", value)
		}
	})

	t.Run("value round-trips", func(t *testing.T) {
		s := newStore(t)
		want := []byte("payload")
		if err := s.Set(context.Background(), "k", want, 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, present, err := s.Get(context.Background(), "k")
		if err != nil || !present {
			t.Fatalf("Get after Set: present=%v err=%v", present, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get returned %q, want %q", got, want)
		}
	})

	t.Run("a later Set replaces the value and the expiry", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("first"), liveTTL); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := s.Set(ctx, "k", []byte("second"), 0); err != nil {
			t.Fatalf("second Set: %v", err)
		}
		// The second write carried no ttl, so the first one's must be gone:
		// a store that kept it would drop the key while this case waits.
		time.Sleep(ttlSettle)
		got, present, err := s.Get(ctx, "k")
		if err != nil || !present {
			t.Fatalf("Get after the replacing Set: present=%v err=%v", present, err)
		}
		if !bytes.Equal(got, []byte("second")) {
			t.Fatalf("Get returned %q, want %q", got, "second")
		}
	})

	t.Run("Delete removes, and removing an absent key is not an error", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := s.Delete(ctx, "k"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, present, err := s.Get(ctx, "k"); err != nil || present {
			t.Fatalf("Get after Delete: present=%v err=%v", present, err)
		}
		if err := s.Delete(ctx, "k"); err != nil {
			t.Fatalf("Delete on an absent key: %v", err)
		}
	})

	t.Run("a key with a ttl ages out", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v"), shortTTL); err != nil {
			t.Fatalf("Set: %v", err)
		}
		time.Sleep(ttlSettle)
		if _, present, err := s.Get(ctx, "k"); err != nil || present {
			t.Fatalf("Get past the ttl: present=%v err=%v", present, err)
		}
	})

	t.Run("a key with no ttl stays", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		time.Sleep(ttlSettle)
		if _, present, err := s.Get(ctx, "k"); err != nil || !present {
			t.Fatalf("Get on a key with no ttl: present=%v err=%v", present, err)
		}
	})

	t.Run("increment starts from zero", func(t *testing.T) {
		s := newStore(t)
		got, err := s.IncrByFloat(context.Background(), "n", 3)
		if err != nil {
			t.Fatalf("IncrByFloat: %v", err)
		}
		if got != 3 {
			t.Fatalf("IncrByFloat returned %v, want 3", got)
		}
	})

	t.Run("increment leaves an existing key's expiry alone", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		// This case needs a ttl long enough to survive the increment and still
		// leave a wait that can tell the two outcomes apart, so it does not use
		// the short one. The wait after the increment runs past the ttl the key
		// was created with but not past a ttl restarted at the increment: a
		// store that extended the key when it was incremented still holds it
		// there, a store that left it alone does not.
		const ttl = 300 * time.Millisecond
		if err := s.Set(ctx, "n", []byte("1"), ttl); err != nil {
			t.Fatalf("Set: %v", err)
		}
		time.Sleep(ttl / 2)
		if _, err := s.IncrByFloat(ctx, "n", 1); err != nil {
			t.Fatalf("IncrByFloat: %v", err)
		}
		time.Sleep(ttl/2 + 100*time.Millisecond)
		if _, present, err := s.Get(ctx, "n"); err != nil || present {
			t.Fatalf("Get past the ttl the key was created with: present=%v err=%v", present, err)
		}
	})

	t.Run("increment with a ttl attaches it to a key it creates", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if _, err := s.IncrByFloatWithTTL(ctx, "n", 1, shortTTL); err != nil {
			t.Fatalf("IncrByFloatWithTTL: %v", err)
		}
		time.Sleep(ttlSettle)
		if _, present, err := s.Get(ctx, "n"); err != nil || present {
			t.Fatalf("Get past the attached ttl: present=%v err=%v", present, err)
		}
	})

	t.Run("increment with a ttl ignores it for a key that exists", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if _, err := s.IncrByFloat(ctx, "n", 1); err != nil {
			t.Fatalf("IncrByFloat: %v", err)
		}
		if _, err := s.IncrByFloatWithTTL(ctx, "n", 1, shortTTL); err != nil {
			t.Fatalf("IncrByFloatWithTTL: %v", err)
		}
		// The key already existed and had no expiry, so it keeps having none.
		time.Sleep(ttlSettle)
		if _, present, err := s.Get(ctx, "n"); err != nil || !present {
			t.Fatalf("Get after a ttl the key must have ignored: present=%v err=%v", present, err)
		}
	})

	t.Run("increment refuses a non-numeric value and leaves it alone", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("not a number"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if _, err := s.IncrByFloat(ctx, "k", 1); !errors.Is(err, kv.ErrNotNumeric) {
			t.Fatalf("IncrByFloat on a non-numeric value: %v, want ErrNotNumeric", err)
		}
		got, present, err := s.Get(ctx, "k")
		if err != nil || !present {
			t.Fatalf("Get after the refused increment: present=%v err=%v", present, err)
		}
		if !bytes.Equal(got, []byte("not a number")) {
			t.Fatalf("the refused increment changed the value to %q", got)
		}
	})

	t.Run("compare-and-swap writes only on a match", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("old"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		swapped, err := s.CompareAndSwap(ctx, "k", []byte("wrong"), []byte("new"))
		if err != nil {
			t.Fatalf("CompareAndSwap: %v", err)
		}
		if swapped {
			t.Fatal("CompareAndSwap swapped on a mismatch")
		}
		if got, _, _ := s.Get(ctx, "k"); !bytes.Equal(got, []byte("old")) {
			t.Fatalf("a refused swap still wrote: the value is %q", got)
		}
		swapped, err = s.CompareAndSwap(ctx, "k", []byte("old"), []byte("new"))
		if err != nil {
			t.Fatalf("CompareAndSwap: %v", err)
		}
		if !swapped {
			t.Fatal("CompareAndSwap refused a match")
		}
		if got, _, _ := s.Get(ctx, "k"); !bytes.Equal(got, []byte("new")) {
			t.Fatalf("the value after a swap is %q, want %q", got, "new")
		}
	})

	t.Run("compare-and-swap doubles as set-if-absent", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for _, absent := range [][]byte{nil, {}} {
			swapped, err := s.CompareAndSwap(ctx, "k", absent, []byte("first"))
			if err != nil {
				t.Fatalf("CompareAndSwap with old=%v: %v", absent, err)
			}
			if !swapped {
				t.Fatalf("CompareAndSwap treated an absent key as a mismatch against old=%v", absent)
			}
			if err := s.Delete(ctx, "k"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
		}
	})

	t.Run("compare-and-swap leaves the expiry alone", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("old"), shortTTL); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if swapped, err := s.CompareAndSwap(ctx, "k", []byte("old"), []byte("new")); err != nil || !swapped {
			t.Fatalf("CompareAndSwap: swapped=%v err=%v", swapped, err)
		}
		time.Sleep(ttlSettle)
		if _, present, err := s.Get(ctx, "k"); err != nil || present {
			t.Fatalf("Get past the original ttl: present=%v err=%v", present, err)
		}
	})

	t.Run("the store does not hand out or retain the caller's slices", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		written := []byte("stored")
		if err := s.Set(ctx, "k", written, 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		copy(written, []byte("XXXXXX"))
		if got, _, _ := s.Get(ctx, "k"); !bytes.Equal(got, []byte("stored")) {
			t.Fatalf("writing into the slice handed to Set changed the stored value to %q", got)
		}
		got, _, err := s.Get(ctx, "k")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		copy(got, []byte("YYYYYY"))
		if again, _, _ := s.Get(ctx, "k"); !bytes.Equal(again, []byte("stored")) {
			t.Fatalf("writing into the slice Get returned changed the stored value to %q", again)
		}
	})

	t.Run("a cancelled context is honoured", func(t *testing.T) {
		s := newStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, context.Canceled) {
			t.Errorf("Set: %v, want context.Canceled", err)
		}
		if _, _, err := s.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
			t.Errorf("Get: %v, want context.Canceled", err)
		}
		if err := s.Delete(ctx, "k"); !errors.Is(err, context.Canceled) {
			t.Errorf("Delete: %v, want context.Canceled", err)
		}
		if _, err := s.IncrByFloat(ctx, "k", 1); !errors.Is(err, context.Canceled) {
			t.Errorf("IncrByFloat: %v, want context.Canceled", err)
		}
		if _, err := s.IncrByFloatWithTTL(ctx, "k", 1, 0); !errors.Is(err, context.Canceled) {
			t.Errorf("IncrByFloatWithTTL: %v, want context.Canceled", err)
		}
		if _, err := s.CompareAndSwap(ctx, "k", nil, []byte("v")); !errors.Is(err, context.Canceled) {
			t.Errorf("CompareAndSwap: %v, want context.Canceled", err)
		}
	})

	t.Run("concurrent increments lose no update", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		const goroutines, each = 50, 20
		var wg sync.WaitGroup
		errs := make(chan error, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < each; j++ {
					if _, err := s.IncrByFloat(ctx, "n", 1); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent IncrByFloat: %v", err)
		}
		got, present, err := s.Get(ctx, "n")
		if err != nil || !present {
			t.Fatalf("Get after concurrent increments: present=%v err=%v", present, err)
		}
		n, err := strconv.ParseFloat(string(got), 64)
		if err != nil {
			t.Fatalf("the counter holds %q, which is not a number", got)
		}
		if want := float64(goroutines * each); n != want {
			t.Fatalf("the counter reached %v, want %v — increments were lost", n, want)
		}
	})

	t.Run("concurrent set-if-absent has exactly one winner", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		const goroutines = 30
		var wg sync.WaitGroup
		wins := make(chan string, goroutines)
		errs := make(chan error, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				mine := []byte(fmt.Sprintf("writer-%d", i))
				swapped, err := s.CompareAndSwap(ctx, "k", nil, mine)
				if err != nil {
					errs <- err
					return
				}
				if swapped {
					wins <- string(mine)
				}
			}(i)
		}
		wg.Wait()
		close(wins)
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent CompareAndSwap: %v", err)
		}
		var winners []string
		for w := range wins {
			winners = append(winners, w)
		}
		if len(winners) != 1 {
			t.Fatalf("%d writers won set-if-absent, want exactly 1: %v", len(winners), winners)
		}
		// The winner is the one whose value is there, not merely one of them
		// having reported success.
		got, present, err := s.Get(ctx, "k")
		if err != nil || !present {
			t.Fatalf("Get after the race: present=%v err=%v", present, err)
		}
		if string(got) != winners[0] {
			t.Fatalf("the stored value is %q but %q reported winning", got, winners[0])
		}
	})
}
