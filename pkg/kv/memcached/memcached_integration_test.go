//go:build integration

package memcached

import (
	"context"
	"testing"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/expiry"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
	"github.com/vislake/speed/pkg/kv/internal/memcachetest"
)

// newStore opens a store on the shared server, in a key space of the case's
// own. It is what every case here that writes to the server works in, the
// contract's subtests included.
func newStore(t *testing.T) *store {
	t.Helper()
	return openOn(t, memcachetest.Addr(t), memcachetest.Space(t))
}

// openOn opens a store over the server at addr, in the key space given.
func openOn(t *testing.T, addr, space string) *store {
	t.Helper()
	opened, err := open(context.Background(), &Config{Servers: []string{addr}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	s.space = space
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// TestConforms runs the shared contract against a real server.
//
// Memcached has no logical databases, so every subtest gets a store in a key
// space of its own rather than a server. No entry such a store can read was
// written by anyone else, which is the whole of what the contract's promise of
// an empty store means here; the server itself is never emptied, because other
// test binaries on the machine are using it at the same moment.
func TestConforms(t *testing.T) {
	kvsuite.AssertConforms(t, func(t *testing.T) kv.KV {
		t.Helper()
		return newStore(t)
	})
}

// TestCasesSharingTheServerNeverMeet pins the isolation the shared server
// needs. One server serves every test binary on the machine, this checkout's
// and every other's, so a case that emptied it, or wrote under the names
// another case writes under, would change entries out from under a case
// running elsewhere at that moment — which fails there as a key that vanished
// or holds a value nothing in that case wrote.
//
// The entry written straight through a client stands for another run's data,
// and the two stores are what two cases get.
func TestCasesSharingTheServerNeverMeet(t *testing.T) {
	addr := memcachetest.Addr(t)
	ctx := context.Background()

	other := memcache.New(addr)
	t.Cleanup(func() { _ = other.Close() })
	theirs := memcachetest.Space(t) + "theirs"
	if err := other.Set(&memcache.Item{Key: theirs, Value: []byte("theirs")}); err != nil {
		t.Fatalf("writing another run's entry: %v", err)
	}
	t.Cleanup(func() { _ = other.Delete(theirs) })

	first := newStore(t)
	if err := first.Set(ctx, "k", []byte("first"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	second := newStore(t)
	if got, present, err := second.Get(ctx, "k"); err != nil || present {
		t.Fatalf("a case reads %q (present=%v, err=%v) under a key only another case wrote", got, present, err)
	}
	if err := second.Set(ctx, "k", []byte("second"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, present, err := first.Get(ctx, "k"); err != nil || !present || string(got) != "first" {
		t.Fatalf("a case's key reads %q (present=%v, err=%v) after another case wrote the same key, "+
			"want %q", got, present, err, "first")
	}
	if item, err := other.Get(theirs); err != nil || string(item.Value) != "theirs" {
		t.Fatalf("another run's entry on the shared server did not survive two cases starting: %v", err)
	}
}

// TestAPaddedServerEntryStillReachesTheClient is the half a stance cannot hold
// on its own. The guard judges an entry by its trimmed text, so an entry
// written with surrounding whitespace passes it — and gomemcache then resolves
// every address it is given and discards the whole list when one fails to
// resolve, which is what a padded address does. Handing the client the text as
// written would leave it holding no server at all: the process runs, every
// operation fails with "no servers configured or available", and nothing names
// the input.
//
// The case pads the address of a real server: the open's check reaches it, and
// an operation afterwards is served.
func TestAPaddedServerEntryStillReachesTheClient(t *testing.T) {
	addr := memcachetest.Addr(t)
	opened, err := open(context.Background(), &Config{
		Servers: []string{" " + addr + " "},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("opening against a padded address: %v", err)
	}
	s := opened.(*store)
	s.space = memcachetest.Space(t)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if _, _, err := s.Get(context.Background(), "padded"); err != nil {
		t.Fatalf("an operation through a padded address: %v", err)
	}
}

// TestTheExpiryTravelsInsideTheValue pins the design this backend forced: a
// compare-and-swap write cannot carry the old expiry forward, so the expiry is
// not in Memcached's expiration field at all and this package is what decides a
// key has aged out.
//
// The case asserts on the header directly rather than through Get, because Get
// would pass whether the expiry worked or the key had simply been deleted.
func TestTheExpiryTravelsInsideTheValue(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	item, err := s.client.Get(s.spell("k"))
	if err != nil {
		t.Fatalf("reading the raw entry: %v", err)
	}
	if len(item.Value) < expiry.Width {
		t.Fatalf("the stored value is %d bytes, too short to hold the header", len(item.Value))
	}
	_, expires, live := expiry.UnwrapFull(item.Value)
	if !live {
		t.Fatal("a key set with a minute to live reads as expired")
	}
	if expires.IsZero() {
		t.Fatal("the expiry is not in the value, so nothing here can keep it across a swap")
	}
	// And the entry is not relying on Memcached's own expiration, which would
	// have dropped it without this package ever seeing it.
	if item.Expiration != 0 {
		t.Fatalf("the entry carries Memcached expiration %d, and this implementation keeps the "+
			"expiry itself", item.Expiration)
	}
}

// TestCompareAndSwapSurvivesTheKeyVanishingUnderIt pins the sentinel handling
// for an entry deleted or evicted between the read and the write: Memcached
// reports the vanished key as a cache miss, and a miss is contention — the swap
// did not happen — not a failure.
//
// The race is driven rather than forced: a delete-and-rewrite loop runs against
// the key while the swaps run, and every outcome has to be one of the two
// documented answers.
func TestCompareAndSwapSurvivesTheKeyVanishingUnderIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const key = "contended"

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Kept in a matching state so the swaps stay on the path that
			// reads, compares and writes back — the path whose write the
			// vanishing races.
			_ = s.Set(ctx, key, []byte("v"), 0)
			_ = s.Delete(ctx, key)
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.CompareAndSwap(ctx, key, []byte("v"), []byte("w")); err != nil {
			close(stop)
			<-done
			t.Fatalf("CompareAndSwap failed while the key was being deleted and rewritten: %v", err)
		}
	}
	close(stop)
	<-done
}

// TestIncrementSurvivesTheKeyVanishingUnderIt is the increment's half of the
// same pin: the capability says a missing key counts from zero, and a key that
// vanishes between the read and the write is exactly that — the retry reads it
// again and finds nothing to count from but zero.
func TestIncrementSurvivesTheKeyVanishingUnderIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const key = "contended-counter"

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = s.Set(ctx, key, []byte("1"), 0)
			_ = s.Delete(ctx, key)
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.IncrByFloat(ctx, key, 1); err != nil {
			close(stop)
			<-done
			t.Fatalf("IncrByFloat failed while the key was being deleted and rewritten: %v", err)
		}
	}
	close(stop)
	<-done
}
