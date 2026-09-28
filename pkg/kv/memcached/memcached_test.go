package memcached

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

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
// server. gomemcache connects lazily and reads an empty address as the zero
// port rather than skipping it, so an entry that is empty or only whitespace
// would open cleanly and then fail every operation that lands on it, with
// nothing at the startup naming the input. One such entry is enough to fail
// the startup, whether or not the list also holds a real address.
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

// TestAPaddedServerEntryStillReachesTheClient is the half a stance cannot hold
// on its own. The guard judges an entry by its trimmed text, so an entry
// written with surrounding whitespace passes it — and gomemcache then resolves
// every address it is given and discards the whole list when one fails to
// resolve, which is what a padded address does. Handing the client the text as
// written would leave it holding no server at all: the process runs, every
// operation fails with "no servers configured or available", and nothing names
// the input.
//
// The address below has nothing listening, so the operation fails either way.
// What the case pins is which failure: a client that kept the address reports
// the connection attempt, and one that discarded the list reports that it has
// no servers.
func TestAPaddedServerEntryStillReachesTheClient(t *testing.T) {
	opened, err := open(context.Background(), &Config{
		Servers: []string{" 127.0.0.1:1 "},
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("opening against a padded address: %v", err)
	}
	if _, _, err := opened.Get(context.Background(), "padded"); errors.Is(err, memcache.ErrNoServers) {
		t.Fatalf("the client was built without the address the host named: %v", err)
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

// TestEveryKeyHasASpellingMemcachedAccepts pins the spelling rule at the
// boundary the contract suite only crosses from far away: a key whose plain
// spelling is exactly the server's limit keeps it, one byte more switches to
// the digest, and the empty key — whose plain spelling is empty and refused —
// gets the digest too. Every spelling is one the client and the server accept.
func TestEveryKeyHasASpellingMemcachedAccepts(t *testing.T) {
	// 187 raw bytes are 250 base64url characters; 188 are 251.
	atLimit, pastLimit := strings.Repeat("a", 187), strings.Repeat("a", 188)
	if got := encodeKey(atLimit); strings.HasPrefix(got, digestPrefix) || len(got) != maxKeyLen {
		t.Fatalf("a key whose plain spelling fits exactly was spelled %q", got)
	}
	for _, key := range []string{"", pastLimit, strings.Repeat("\x00", 10000)} {
		got := encodeKey(key)
		if !strings.HasPrefix(got, digestPrefix) {
			t.Errorf("the key of %d bytes was not spelled by its digest: %q", len(key), got)
		}
		if got == "" || len(got) > maxKeyLen || strings.ContainsAny(got, " \t\r\n\x00") {
			t.Errorf("the key of %d bytes is spelled %q, which Memcached refuses", len(key), got)
		}
	}
	if encodeKey(pastLimit) == encodeKey(pastLimit+"a") {
		t.Fatal("two long keys differing in their last byte share a spelling")
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
