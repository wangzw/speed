package memcached

import (
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/vislake/speed/pkg/core"
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

// TestStanceFailsTheStartupWithoutAServer pins the rule that a module named in
// the command tree has to start: this one reaches Memcached at a server, so
// being named without one stops the startup and names the input it stopped on.
// A stand-down in its place would bring the process up with the capability
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

// TestAnUnreachableServerFailsTheStartup pins the stage the capability puts a
// connection failure in. The client connects lazily, so without a check at
// open a mistyped or unreachable server passes the startup, the process
// reports ready, and the failure lands on whichever request uses the store
// first.
func TestAnUnreachableServerFailsTheStartup(t *testing.T) {
	// A port that was just free and has nothing listening on it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	opened, err := open(context.Background(), &Config{Servers: []string{addr}, Timeout: time.Second})
	if err == nil {
		_ = opened.(*store).Close(context.Background())
		t.Fatalf("opening against %s, where nothing listens, succeeded", addr)
	}
	if !strings.Contains(err.Error(), addr) {
		t.Fatalf("the startup failure does not name the server it could not reach: %v", err)
	}
}

// TestCloseReleasesTheConnectionsTheClientKeeps pins the Close stage: the
// connections this module opened are closed by this module. The client keeps
// idle connections between operations, so a Close that released nothing would
// leave them open on the server after the module shut down — for as long as
// the process lives, in a host or a test binary that outlives the module.
func TestCloseReleasesTheConnectionsTheClientKeeps(t *testing.T) {
	server := startFakeServer(t)
	opened, err := open(context.Background(), &Config{Servers: []string{server.addr()}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, _, getErr := opened.Get(context.Background(), "k"); getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if closeErr := opened.(*store).Close(context.Background()); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for server.closed.Load() < server.accepted.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("after Close the client still holds %d of the %d connections it opened",
				server.accepted.Load()-server.closed.Load(), server.accepted.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBurstsOfOperationsReuseTheirConnections covers what a busy replica does
// to its sockets. The client keeps only so many idle connections per server
// and closes every connection past that when an operation returns it, so each
// burst of concurrent operations — a wave of requests arriving together — ends
// with most of its connections closed and the next burst dialling them again,
// each close leaving a socket in TIME_WAIT. Under sustained bursty load the
// replica runs out of local ports and operations fail to dial. The case sends
// bursts of a modest width and counts the connections the server accepted.
func TestBurstsOfOperationsReuseTheirConnections(t *testing.T) {
	server := startFakeServer(t)
	opened, err := open(context.Background(), &Config{
		Servers: []string{server.addr()},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = opened.(*store).Close(context.Background()) })

	const width, bursts = 16, 20
	for range bursts {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range width {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, _, getErr := opened.Get(context.Background(), "k"); getErr != nil {
					t.Errorf("Get: %v", getErr)
				}
			}()
		}
		close(start)
		wg.Wait()
	}
	// One connection per operation of the widest burst at most, and one for
	// the check the open makes.
	if got := server.accepted.Load(); got > width+1 {
		t.Fatalf("%d bursts of %d operations made the server accept %d connections: the client closes "+
			"the connections a burst used and dials them again for the next", bursts, width, got)
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

// TestEveryKeyHasASpellingMemcachedAccepts pins the spelling rule at the
// boundary the contract suite only crosses from far away: a key whose plain
// spelling is exactly the server's limit keeps it, one byte more switches to
// the digest, and the empty key — whose plain spelling is empty and refused —
// gets the digest too. Every spelling is one the client and the server accept.
//
// The store here has no space, as every store a host opens has none.
func TestEveryKeyHasASpellingMemcachedAccepts(t *testing.T) {
	s := &store{}
	// 187 raw bytes are 250 base64url characters; 188 are 251.
	atLimit, pastLimit := strings.Repeat("a", 187), strings.Repeat("a", 188)
	if got := s.spell(atLimit); strings.HasPrefix(got, digestPrefix) || len(got) != maxKeyLen {
		t.Fatalf("a key whose plain spelling fits exactly was spelled %q", got)
	}
	for _, key := range []string{"", pastLimit, strings.Repeat("\x00", 10000)} {
		got := s.spell(key)
		if !strings.HasPrefix(got, digestPrefix) {
			t.Errorf("the key of %d bytes was not spelled by its digest: %q", len(key), got)
		}
		if got == "" || len(got) > maxKeyLen || strings.ContainsAny(got, " \t\r\n\x00") {
			t.Errorf("the key of %d bytes is spelled %q, which Memcached refuses", len(key), got)
		}
	}
	if s.spell(pastLimit) == s.spell(pastLimit+"a") {
		t.Fatal("two long keys differing in their last byte share a spelling")
	}
}

// TestASpaceLeavesEverySpellingWithinTheLimit is the same rule for a store in
// a key space, which is what every case against the shared server works in.
// The space comes first and takes its length out of the server's limit, so a
// key whose plain spelling would fit on its own but not behind the space
// switches to the digest; spelling it plainly would hand the server a name
// longer than it accepts, and the operation would fail.
func TestASpaceLeavesEverySpellingWithinTheLimit(t *testing.T) {
	s := &store{space: memcachetest.Space(t)}
	room := maxKeyLen - len(s.space)
	for n := range 300 {
		key := strings.Repeat("a", n)
		got := s.spell(key)
		if len(got) > maxKeyLen {
			t.Fatalf("the key of %d bytes is spelled in %d bytes behind the space, past the server's "+
				"limit of %d", n, len(got), maxKeyLen)
		}
		rest, inSpace := strings.CutPrefix(got, s.space)
		if !inSpace {
			t.Fatalf("the key of %d bytes is spelled %q, outside the store's space %q", n, got, s.space)
		}
		// A space ends at the first dot of an entry's name, so a spelling
		// holding one would read as the end of a different space.
		if strings.Contains(rest, ".") {
			t.Fatalf("the key of %d bytes is spelled %q behind the space, and a dot there reads as "+
				"another space's end", n, rest)
		}
		wantDigest := n == 0 || base64.RawURLEncoding.EncodedLen(n) > room
		if digested := strings.HasPrefix(rest, digestPrefix); digested != wantDigest {
			t.Fatalf("the key of %d bytes, with %d bytes of room behind the space, was spelled %q",
				n, room, got)
		}
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
