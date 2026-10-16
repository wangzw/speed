package memcached

import (
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv/internal/memcachetest"
)

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
