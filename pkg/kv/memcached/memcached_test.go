package memcached

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/expiry"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
	"github.com/vislake/speed/pkg/kv/internal/memcachetest"
)

func newStore(t *testing.T) *store {
	t.Helper()
	addr := memcachetest.Addr(t)
	opened, err := open(context.Background(), &Config{Servers: []string{addr}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// TestConforms runs the shared contract against a real server.
//
// Memcached has no logical databases, so the server is flushed between subtests
// rather than one being handed out per case. That is the whole of what the
// contract's factory promise means here.
func TestConforms(t *testing.T) {
	s := newStore(t)
	kvsuite.AssertConforms(t, func(t *testing.T) kv.KV {
		t.Helper()
		if err := s.client.FlushAll(); err != nil {
			t.Fatalf("flushing the server: %v", err)
		}
		return s
	})
}

// TestStanceFailsTheStartupWithoutAServer pins the rule that a module named in
// the command tree has to start: this one reaches Memcached at a server, so
// being named without one stops the startup and names the input it stopped on.
// Under the stand-down it replaces, the process came up with the capability
// silently missing.
func TestStanceFailsTheStartupWithoutAServer(t *testing.T) {
	_, err := stance(&Config{})
	if err == nil {
		t.Fatal("naming this implementation without a server started anyway")
	}
	if want := ConfigNamespace + "." + serversKey; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

// TestStanceRefusesABlankServerEntry covers the other shape of a missing
// server. The addresses are handed to gomemcache as they are written, and its
// client connects lazily and reads an empty address as the zero port rather
// than skipping it: an entry that is empty or only whitespace would therefore
// open cleanly and then fail every operation that lands on it, with nothing at
// the startup naming the input. One such entry is enough to fail the startup,
// whether or not the list also holds a real address.
func TestStanceRefusesABlankServerEntry(t *testing.T) {
	for _, servers := range [][]string{
		{""},
		{"  "},
		{"\t"},
		{"", "  "},
		{"", "127.0.0.1:11211"},
		{"127.0.0.1:11211", "\t"},
	} {
		_, err := stance(&Config{Servers: servers})
		if err == nil {
			t.Errorf("stance with servers %q started anyway, and every operation against a blank "+
				"address fails", servers)
			continue
		}
		if want := ConfigNamespace + "." + serversKey; !strings.Contains(err.Error(), want) {
			t.Errorf("the error for servers %q does not name %q: %v", servers, want, err)
		}
	}
	for _, servers := range [][]string{{"127.0.0.1:11211"}, {"127.0.0.1:11211", "127.0.0.1:11212"}} {
		got, err := stance(&Config{Servers: servers})
		if err != nil {
			t.Errorf("stance with servers %q failed the startup: %v", servers, err)
			continue
		}
		if got.State != core.StateEnabled {
			t.Errorf("stance with servers %q = %v, want StateEnabled", servers, got.State)
		}
	}
}

// TestStanceFailsTheStartupWhenTheDataHasToBeKept is the deployment
// precondition. This is the one backend here whose data can vanish without a
// restart, and the failure it would otherwise cause is silent: entries are
// dropped under memory pressure as normal operation and the server does not
// report that it has. An assembly that declares the data has to be kept
// therefore stops the startup, naming the declaration rather than a setting.
func TestStanceFailsTheStartupWhenTheDataHasToBeKept(t *testing.T) {
	_, err := stance(&Config{Servers: []string{"127.0.0.1:11211"}, RequiresPersistence: true})
	if err == nil {
		t.Fatal("a deployment that declared its data has to survive a restart was accepted")
	}
	if want := ConfigNamespace + "." + persistenceKey; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	got, err := stance(&Config{Servers: []string{"127.0.0.1:11211"}})
	if err != nil {
		t.Fatalf("stance with a server: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a server = %v, want StateEnabled", got.State)
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
	item, err := s.client.Get(encodeKey("k"))
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

// TestAKeyMemcachedCannotHoldIsRefusedRatherThanTruncated covers the one place
// this backend's limits reach a caller: the encoding is injective, so a key
// that runs past the server's limit is refused by the server rather than
// silently sharing an entry with another.
func TestAKeyMemcachedCannotHoldIsRefusedRatherThanTruncated(t *testing.T) {
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'a'
	}
	encoded := encodeKey(string(long))
	if encoded == encodeKey(string(long[:len(long)-1])) {
		t.Fatal("two different keys encode to the same spelling, so one would overwrite the other")
	}
	if len(encoded) <= 250 {
		t.Skip("the encoding fits the server's limit, so there is nothing to refuse here")
	}
	s := newStore(t)
	if err := s.Set(context.Background(), string(long), []byte("v"), 0); err == nil {
		t.Fatal("a key past the server's limit was accepted")
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
