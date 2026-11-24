// Package kvsuite holds the contract every implementation of pkg/kv must satisfy, run by each backend through AssertConforms.
package kvsuite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/kv"
)

// Factory builds a fresh, empty store for one subtest.
// Each call returns a store with no relation to the ones before it.
type Factory func(t *testing.T) kv.KV

const ttlSettle = 150 * time.Millisecond

const shortTTL = 30 * time.Millisecond

const contendedTTL = 500 * time.Millisecond

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

	t.Run("a nil value is stored as an empty one", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "set", nil, 0); err != nil {
			t.Fatalf("Set with a nil value: %v", err)
		}
		if got, present, err := s.Get(ctx, "set"); err != nil || !present || len(got) != 0 {
			t.Fatalf("Get after a nil Set: %q, present=%v, err=%v", got, present, err)
		}
		if swapped, err := s.CompareAndSwap(ctx, "swapped", nil, nil); err != nil || !swapped {
			t.Fatalf("CompareAndSwap from absent to nil: swapped=%v err=%v", swapped, err)
		}
		if got, present, err := s.Get(ctx, "swapped"); err != nil || !present || len(got) != 0 {
			t.Fatalf("Get after a swap to nil: %q, present=%v, err=%v", got, present, err)
		}
		if err := s.Set(ctx, "replaced", []byte("v"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if swapped, err := s.CompareAndSwap(ctx, "replaced", []byte("v"), nil); err != nil || !swapped {
			t.Fatalf("CompareAndSwap from a value to nil: swapped=%v err=%v", swapped, err)
		}
		if got, present, err := s.Get(ctx, "replaced"); err != nil || !present || len(got) != 0 {
			t.Fatalf("Get after a swap from a value to nil: %q, present=%v, err=%v", got, present, err)
		}
	})

	t.Run("every key the contract allows is its own entry", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		keys := keysTheContractAllows()
		for i, key := range keys {
			if err := s.Set(ctx, key, []byte(strconv.Itoa(i)), 0); err != nil {
				t.Fatalf("Set on key %s: %v", describeKey(key), err)
			}
		}
		for i, key := range keys {
			got, present, err := s.Get(ctx, key)
			if err != nil || !present {
				t.Fatalf("Get on key %s: present=%v err=%v", describeKey(key), present, err)
			}
			if want := strconv.Itoa(i); string(got) != want {
				t.Fatalf("key %s reads %q, want %q: two keys share one entry", describeKey(key), got, want)
			}
		}
		for _, key := range keys {
			if swapped, err := s.CompareAndSwap(ctx, key, nil, []byte("x")); err != nil || swapped {
				t.Fatalf("CompareAndSwap from absent on the present key %s: swapped=%v err=%v",
					describeKey(key), swapped, err)
			}
			if stored, err := s.SetIfAbsent(ctx, key, []byte("x"), 0); err != nil || stored {
				t.Fatalf("SetIfAbsent on the present key %s: stored=%v err=%v", describeKey(key), stored, err)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatalf("Delete on key %s: %v", describeKey(key), err)
			}
			if _, present, err := s.Get(ctx, key); err != nil || present {
				t.Fatalf("Get after Delete on key %s: present=%v err=%v", describeKey(key), present, err)
			}
			if stored, err := s.SetIfAbsent(ctx, key, []byte("y"), 0); err != nil || !stored {
				t.Fatalf("SetIfAbsent on the deleted key %s: stored=%v err=%v", describeKey(key), stored, err)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatalf("Delete on key %s: %v", describeKey(key), err)
			}
			if got, err := s.IncrByFloat(ctx, key, 2); err != nil || got != 2 {
				t.Fatalf("IncrByFloat on key %s: got=%v err=%v", describeKey(key), got, err)
			}
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

	t.Run("an increment that would leave no finite number is refused and changes nothing", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		increments := map[string]func(key string, delta float64) error{
			"IncrByFloat": func(key string, delta float64) error {
				_, err := s.IncrByFloat(ctx, key, delta)
				return err
			},
			"IncrByFloatWithTTL": func(key string, delta float64) error {
				_, err := s.IncrByFloatWithTTL(ctx, key, delta, liveTTL)
				return err
			},
		}
		for name, incr := range increments {
			for _, delta := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
				err := incr("absent", delta)
				if err == nil {
					t.Fatalf("%s by %v succeeded", name, delta)
				}
				if errors.Is(err, kv.ErrNotNumeric) {
					t.Fatalf("%s by %v on an absent key blamed the stored value: %v", name, delta, err)
				}
				if _, present, getErr := s.Get(ctx, "absent"); getErr != nil || present {
					t.Fatalf("%s by %v created the key: present=%v err=%v", name, delta, present, getErr)
				}
			}
			const largest = "1.7976931348623157e308"
			if err := s.Set(ctx, "full", []byte(largest), 0); err != nil {
				t.Fatalf("Set: %v", err)
			}
			err := incr("full", math.MaxFloat64)
			if err == nil {
				t.Fatalf("%s past float64's range succeeded", name)
			}
			if errors.Is(err, kv.ErrNotNumeric) {
				t.Fatalf("%s past float64's range blamed the stored value, which is a number: %v", name, err)
			}
			if got, _, getErr := s.Get(ctx, "full"); getErr != nil || string(got) != largest {
				t.Fatalf("%s past float64's range changed the counter to %q (err %v)", name, got, getErr)
			}
		}
	})

	t.Run("the non-numeric error leaves the stored value out of its text", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for _, stored := range []string{"alice@example.com", "\xffalice@example.com"} {
			if err := s.Set(ctx, "k", []byte(stored), 0); err != nil {
				t.Fatalf("Set: %v", err)
			}
			for name, incr := range map[string]func() error{
				"IncrByFloat": func() error { _, err := s.IncrByFloat(ctx, "k", 1); return err },
				"IncrByFloatWithTTL": func() error {
					_, err := s.IncrByFloatWithTTL(ctx, "k", 1, liveTTL)
					return err
				},
			} {
				err := incr()
				if !errors.Is(err, kv.ErrNotNumeric) {
					t.Fatalf("%s on %q: %v, want ErrNotNumeric", name, stored, err)
				}
				if strings.Contains(err.Error(), "alice") {
					t.Fatalf("%s on a non-numeric value names it in its error: %v", name, err)
				}
			}
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
		claimed := []byte("claimed")
		if stored, err := s.SetIfAbsent(ctx, "claim", claimed, 0); err != nil || !stored {
			t.Fatalf("SetIfAbsent: stored=%v err=%v", stored, err)
		}
		copy(claimed, []byte("XXXXXXX"))
		if got, _, _ := s.Get(ctx, "claim"); !bytes.Equal(got, []byte("claimed")) {
			t.Fatalf("writing into the slice handed to SetIfAbsent changed the stored value to %q", got)
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
		if _, err := s.SetIfAbsent(ctx, "k", []byte("v"), 0); !errors.Is(err, context.Canceled) {
			t.Errorf("SetIfAbsent: %v, want context.Canceled", err)
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

	t.Run("an expired key is incremented from zero and takes the new expiry", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for _, key := range []string{"plain", "with-ttl"} {
			if err := s.Set(ctx, key, []byte("5"), shortTTL); err != nil {
				t.Fatalf("Set: %v", err)
			}
		}
		time.Sleep(ttlSettle)
		if got, err := s.IncrByFloat(ctx, "plain", 1); err != nil || got != 1 {
			t.Fatalf("IncrByFloat on an expired key: got=%v err=%v, want 1", got, err)
		}
		if got, err := s.IncrByFloatWithTTL(ctx, "with-ttl", 1, shortTTL); err != nil || got != 1 {
			t.Fatalf("IncrByFloatWithTTL on an expired key: got=%v err=%v, want 1", got, err)
		}
		time.Sleep(ttlSettle)
		if _, present, err := s.Get(ctx, "plain"); err != nil || !present {
			t.Fatalf("the key IncrByFloat recreated kept the old expiry: present=%v err=%v", present, err)
		}
		if _, present, err := s.Get(ctx, "with-ttl"); err != nil || present {
			t.Fatalf("the key IncrByFloatWithTTL recreated did not take its ttl: present=%v err=%v", present, err)
		}
	})

	t.Run("concurrent increments with a ttl lose no update, on an absent key and on an expired one", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "expired", []byte("100"), time.Millisecond); err != nil {
			t.Fatalf("Set: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
		const goroutines, each = 20, 10
		for _, key := range []string{"absent", "expired"} {
			start := make(chan struct{})
			var wg sync.WaitGroup
			errs := make(chan error, goroutines)
			for range goroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for range each {
						if _, err := s.IncrByFloatWithTTL(ctx, key, 1, liveTTL); err != nil {
							errs <- err
							return
						}
					}
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent IncrByFloatWithTTL on the %s key: %v", key, err)
			}
			got, present, err := s.Get(ctx, key)
			if err != nil || !present {
				t.Fatalf("Get on the %s key: present=%v err=%v", key, present, err)
			}
			if want := strconv.Itoa(goroutines * each); string(got) != want {
				t.Fatalf("the %s key counted to %s, want %s: increments were lost or the expired value was kept",
					key, got, want)
			}
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
		got, present, err := s.Get(ctx, "k")
		if err != nil || !present {
			t.Fatalf("Get after the race: present=%v err=%v", present, err)
		}
		if string(got) != winners[0] {
			t.Fatalf("the stored value is %q but %q reported winning", got, winners[0])
		}
	})

	t.Run("a swap whose match holds throughout always happens", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		const rounds, writers = 20, 8
		race := func(key string, value []byte) {
			start := make(chan struct{})
			var wg sync.WaitGroup
			refused := make(chan error, writers)
			for range writers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					swapped, err := s.CompareAndSwap(ctx, key, value, value)
					if err != nil || !swapped {
						refused <- fmt.Errorf("swapped=%v err=%w", swapped, err)
					}
				}()
			}
			close(start)
			wg.Wait()
			close(refused)
			for err := range refused {
				t.Fatalf("a swap of %q from %q, which always matched, did not happen: %v", key, value, err)
			}
		}
		for range rounds {
			race("k", []byte("v"))
		}
		for round := range rounds {
			race(fmt.Sprintf("absent-%d", round), nil)
		}
	})

	t.Run("concurrent set-if-absent on an expired key has exactly one winner", func(t *testing.T) {
		s := newStore(t)
		raceOnExpiredKeys(t, s, func(key string, mine []byte) (bool, error) {
			return s.CompareAndSwap(context.Background(), key, nil, mine)
		})
	})

	t.Run("SetIfAbsent stores where the key is absent and leaves a live key alone", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if stored, err := s.SetIfAbsent(ctx, "k", []byte("first"), 0); err != nil || !stored {
			t.Fatalf("SetIfAbsent on an absent key: stored=%v err=%v", stored, err)
		}
		if got, present, err := s.Get(ctx, "k"); err != nil || !present || string(got) != "first" {
			t.Fatalf("Get after SetIfAbsent: %q, present=%v, err=%v", got, present, err)
		}
		if stored, err := s.SetIfAbsent(ctx, "k", []byte("second"), shortTTL); err != nil || stored {
			t.Fatalf("SetIfAbsent on a live key: stored=%v err=%v", stored, err)
		}
		time.Sleep(ttlSettle)
		if got, present, err := s.Get(ctx, "k"); err != nil || !present || string(got) != "first" {
			t.Fatalf("Get after a refused SetIfAbsent: %q, present=%v, err=%v", got, present, err)
		}
		if _, err := s.IncrByFloat(ctx, "n", 1); err != nil {
			t.Fatalf("IncrByFloat: %v", err)
		}
		if stored, err := s.SetIfAbsent(ctx, "n", []byte("x"), 0); err != nil || stored {
			t.Fatalf("SetIfAbsent on a counter: stored=%v err=%v", stored, err)
		}
	})

	t.Run("SetIfAbsent stores a nil value as an empty one", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if stored, err := s.SetIfAbsent(ctx, "k", nil, 0); err != nil || !stored {
			t.Fatalf("SetIfAbsent with a nil value: stored=%v err=%v", stored, err)
		}
		if got, present, err := s.Get(ctx, "k"); err != nil || !present || len(got) != 0 {
			t.Fatalf("Get after a nil SetIfAbsent: %q, present=%v, err=%v", got, present, err)
		}
		if stored, err := s.SetIfAbsent(ctx, "k", []byte("x"), 0); err != nil || stored {
			t.Fatalf("SetIfAbsent over an empty value: stored=%v err=%v", stored, err)
		}
	})

	t.Run("concurrent SetIfAbsent on an expired key has exactly one winner", func(t *testing.T) {
		s := newStore(t)
		raceOnExpiredKeys(t, s, func(key string, mine []byte) (bool, error) {
			return s.SetIfAbsent(context.Background(), key, mine, liveTTL)
		})
	})

	assertWithinTTL(t, newStore)
}

// AssertUnderLatency runs the cases that act on a key while its ttl runs against the factory's
// stores with every call arriving late — later than the shortest ttl a case starts with — which is
// what a loaded server does to each round trip.
func AssertUnderLatency(t *testing.T, newStore Factory) {
	t.Helper()
	assertWithinTTL(t, func(t *testing.T) kv.KV { return lagging{KV: newStore(t), lag: 2 * shortTTL} })
}

type lagging struct {
	kv.KV
	lag time.Duration
}

func (l lagging) Get(ctx context.Context, key string) ([]byte, bool, error) {
	time.Sleep(l.lag)
	return l.KV.Get(ctx, key)
}

func (l lagging) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	time.Sleep(l.lag)
	return l.KV.Set(ctx, key, value, ttl)
}

func (l lagging) Delete(ctx context.Context, key string) error {
	time.Sleep(l.lag)
	return l.KV.Delete(ctx, key)
}

func (l lagging) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	time.Sleep(l.lag)
	return l.KV.IncrByFloat(ctx, key, delta)
}

func (l lagging) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	time.Sleep(l.lag)
	return l.KV.IncrByFloatWithTTL(ctx, key, delta, ttl)
}

func (l lagging) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	time.Sleep(l.lag)
	return l.KV.CompareAndSwap(ctx, key, old, newVal)
}

func (l lagging) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	time.Sleep(l.lag)
	return l.KV.SetIfAbsent(ctx, key, value, ttl)
}

const liveAttempts = 6

func whileLive(t *testing.T, first time.Duration, attempt func(key string, ttl time.Duration) (concluded bool)) {
	t.Helper()
	ttl := first
	for n := range liveAttempts {
		if attempt(fmt.Sprintf("live-%d", n), ttl) {
			return
		}
		ttl *= 2
	}
	t.Fatalf("no attempt kept its round trips within its ttl, the last one %s, so the case concluded nothing",
		ttl/2)
}

func sleepPast(written time.Time, ttl time.Duration) {
	time.Sleep(time.Until(written.Add(ttl + ttlSettle)))
}

func assertWithinTTL(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("compare-and-swap leaves the expiry alone", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		whileLive(t, shortTTL, func(key string, ttl time.Duration) bool {
			began := time.Now()
			if err := s.Set(ctx, key, []byte("old"), ttl); err != nil {
				t.Fatalf("Set: %v", err)
			}
			written := time.Now()
			swapped, err := s.CompareAndSwap(ctx, key, []byte("old"), []byte("new"))
			if err != nil {
				t.Fatalf("CompareAndSwap: %v", err)
			}
			if !swapped {
				if time.Since(began) >= ttl {
					return false
				}
				t.Fatalf("CompareAndSwap refused a match on a key within its %s ttl", ttl)
			}
			sleepPast(written, ttl)
			if _, present, err := s.Get(ctx, key); err != nil || present {
				t.Fatalf("Get past the original ttl: present=%v err=%v", present, err)
			}
			return true
		})
	})

	t.Run("increment leaves an existing key's expiry alone", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		whileLive(t, 300*time.Millisecond, func(key string, ttl time.Duration) bool {
			began := time.Now()
			if err := s.Set(ctx, key, []byte("1"), ttl); err != nil {
				t.Fatalf("Set: %v", err)
			}
			written := time.Now()
			time.Sleep(ttl / 2)
			incremented := time.Now()
			got, err := s.IncrByFloat(ctx, key, 1)
			if err != nil {
				t.Fatalf("IncrByFloat: %v", err)
			}
			if time.Since(began) >= ttl {
				return false
			}
			if got != 2 {
				t.Fatalf("IncrByFloat on a key within its %s ttl returned %v, want 2", ttl, got)
			}
			time.Sleep(time.Until(written.Add(ttl + ttl/10)))
			_, present, err := s.Get(ctx, key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if present {
				t.Fatalf("Get past the %s ttl the key was created with found it: the increment extended it", ttl)
			}
			return time.Now().Before(incremented.Add(ttl))
		})
	})

	t.Run("a refused SetIfAbsent does not extend a live key", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		whileLive(t, shortTTL, func(key string, ttl time.Duration) bool {
			began := time.Now()
			if err := s.Set(ctx, key, []byte("v"), ttl); err != nil {
				t.Fatalf("Set: %v", err)
			}
			written := time.Now()
			stored, err := s.SetIfAbsent(ctx, key, []byte("x"), liveTTL)
			if err != nil {
				t.Fatalf("SetIfAbsent: %v", err)
			}
			if stored {
				if time.Since(began) >= ttl {
					return false
				}
				t.Fatalf("SetIfAbsent stored over a key within its %s ttl", ttl)
			}
			sleepPast(written, ttl)
			if _, present, err := s.Get(ctx, key); err != nil || present {
				t.Fatalf("a refused SetIfAbsent extended the key: present=%v err=%v", present, err)
			}
			return true
		})
	})

	t.Run("SetIfAbsent attaches its ttl, and an expired key is absent to it", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		whileLive(t, shortTTL, func(key string, ttl time.Duration) bool {
			began := time.Now()
			if stored, err := s.SetIfAbsent(ctx, key, []byte("v"), ttl); err != nil || !stored {
				t.Fatalf("SetIfAbsent: stored=%v err=%v", stored, err)
			}
			written := time.Now()
			got, present, err := s.Get(ctx, key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !present {
				if time.Since(began) >= ttl {
					return false
				}
				t.Fatalf("Get within the %s ttl found nothing", ttl)
			}
			if string(got) != "v" {
				t.Fatalf("Get within the ttl: %q, want %q", got, "v")
			}
			sleepPast(written, ttl)
			if _, present, err := s.Get(ctx, key); err != nil || present {
				t.Fatalf("Get past the ttl: present=%v err=%v", present, err)
			}
			if stored, err := s.SetIfAbsent(ctx, key, []byte("again"), 0); err != nil || !stored {
				t.Fatalf("SetIfAbsent on an expired key: stored=%v err=%v", stored, err)
			}
			time.Sleep(ttlSettle)
			if got, present, err := s.Get(ctx, key); err != nil || !present || string(got) != "again" {
				t.Fatalf("Get after a SetIfAbsent with no ttl: %q, present=%v, err=%v", got, present, err)
			}
			return true
		})
	})

	t.Run("concurrent SetIfAbsent has exactly one winner, whose ttl applies", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		whileLive(t, contendedTTL, func(key string, ttl time.Duration) bool {
			const goroutines = 30
			began := time.Now()
			start := make(chan struct{})
			var wg sync.WaitGroup
			wins := make(chan string, goroutines)
			errs := make(chan error, goroutines)
			for i := range goroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					mine := []byte(fmt.Sprintf("writer-%d", i))
					<-start
					stored, err := s.SetIfAbsent(ctx, key, mine, ttl)
					if err != nil {
						errs <- err
						return
					}
					if stored {
						wins <- string(mine)
					}
				}()
			}
			close(start)
			wg.Wait()
			raced := time.Now()
			close(wins)
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent SetIfAbsent: %v", err)
			}
			var winners []string
			for w := range wins {
				winners = append(winners, w)
			}
			got, present, err := s.Get(ctx, key)
			if err != nil {
				t.Fatalf("Get after the race: %v", err)
			}
			if time.Since(began) >= ttl {
				return false
			}
			if len(winners) != 1 {
				t.Fatalf("%d writers won SetIfAbsent, want exactly 1: %v", len(winners), winners)
			}
			if !present || string(got) != winners[0] {
				t.Fatalf("the key holds %q (present=%v) but %q reported winning", got, present, winners[0])
			}
			sleepPast(raced, ttl)
			if _, present, err := s.Get(ctx, key); err != nil || present {
				t.Fatalf("the winner's ttl did not apply: present=%v err=%v", present, err)
			}
			return true
		})
	})
}

func raceOnExpiredKeys(t *testing.T, s kv.KV, claim func(key string, mine []byte) (bool, error)) {
	t.Helper()
	ctx := context.Background()
	const keys, writers = 100, 8
	for k := range keys {
		if err := s.Set(ctx, fmt.Sprintf("expired-%d", k), []byte("stale"), time.Millisecond); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	for k := range keys {
		key := fmt.Sprintf("expired-%d", k)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wins := make(chan string, writers)
		errs := make(chan error, writers)
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mine := []byte(fmt.Sprintf("writer-%d", w))
				<-start
				won, err := claim(key, mine)
				if err != nil {
					errs <- err
					return
				}
				if won {
					wins <- string(mine)
				}
			}()
		}
		close(start)
		wg.Wait()
		close(wins)
		close(errs)
		for err := range errs {
			t.Fatalf("a claim on the expired key %q failed rather than losing: %v", key, err)
		}
		var winners []string
		for w := range wins {
			winners = append(winners, w)
		}
		if len(winners) != 1 {
			t.Fatalf("%d writers claimed the expired key %q, want exactly 1: %v", len(winners), key, winners)
		}
		got, present, err := s.Get(ctx, key)
		if err != nil || !present || string(got) != winners[0] {
			t.Fatalf("the expired key %q holds %q (present=%v, err=%v) but %q reported winning",
				key, got, present, err, winners[0])
		}
	}
}

func keysTheContractAllows() []string {
	noise := func(n int) string {
		out := make([]byte, 0, n+sha256.Size)
		block := sha256.Sum256(nil)
		for len(out) < n {
			out = append(out, block[:]...)
			block = sha256.Sum256(block[:])
		}
		return string(out[:n])
	}
	long := strings.Repeat("k", 5000)
	wide := noise(4000)
	return []string{
		"",
		"plain",
		"with space",
		"sep:ar/at.ed*and>wild",
		"nul\x00byte",
		"bad\xffutf8",
		"ключ-键-🔑",
		strings.Repeat("m", 200),
		long + "a",
		long + "b",
		wide + "a",
		wide + "b",
	}
}

func describeKey(key string) string {
	if len(key) <= 32 {
		return fmt.Sprintf("%q", key)
	}
	return fmt.Sprintf("%q… (%d bytes)", key[:16], len(key))
}
