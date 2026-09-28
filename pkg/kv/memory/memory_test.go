package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
)

func newStore(t *testing.T) kv.KV {
	t.Helper()
	s, err := open(context.Background(), &Config{Replicas: 1})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

// TestConforms runs the shared contract. Every implementation of the capability
// runs this same suite; what is specific to this backend is tested below and in
// the package's own files.
func TestConforms(t *testing.T) {
	kvsuite.AssertConforms(t, newStore)
}

// TestTheSuiteTellsASlowStoreFromAWrongOne runs the shared suite's cases that
// act on a key within its ttl against this implementation with every call
// arriving late, as a loaded server makes it arrive. This store is correct, so
// a case failing here is a case that reads a slow round trip as a wrong answer,
// and would fail a correct backend on any loaded run.
func TestTheSuiteTellsASlowStoreFromAWrongOne(t *testing.T) {
	kvsuite.AssertUnderLatency(t, newStore)
}

func TestStanceIsAutoWhenOneReplicaSharesTheState(t *testing.T) {
	got, err := stance(&Config{Replicas: 1})
	if err != nil {
		t.Fatalf("stance with one replica: %v", err)
	}
	if got.State != core.StateAuto {
		t.Fatalf("stance with one replica = %v, want StateAuto: this implementation is the "+
			"capability's zero-configuration default and has to yield to an explicitly enabled backend",
			got.State)
	}
}

// TestStanceFailsTheStartupWhenSeveralReplicasShareTheState pins the deployment
// precondition. A host that declared several replicas gets a startup that stops
// and names the input it stopped on: this backend would give each replica its
// own private map, every call would still succeed, and nothing else in the
// process would ever notice. A stand-down would be worse than useless here — it
// would leave the capability missing with the process still coming up.
func TestStanceFailsTheStartupWhenSeveralReplicasShareTheState(t *testing.T) {
	_, err := stance(&Config{Replicas: 3})
	if err == nil {
		t.Fatal("a host that declared three replicas was given a store each replica writes to alone")
	}
	for _, want := range []string{ConfigNamespace + "." + replicasKey, "3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v",
				want, err)
		}
	}
}

// TestExpiredEntriesAreSweptOutOfTheMap pins the active half of expiry: an
// entry whose time passed while nothing asked for it is dropped by a later
// write's sweep, rather than sitting in the map until the key is read again.
// A process that gives every request its own ttl'd key — a rate-limit counter,
// a session-revocation marker — would otherwise grow with every key it has
// ever written.
func TestExpiredEntriesAreSweptOutOfTheMap(t *testing.T) {
	s, ok := newStore(t).(*store)
	if !ok {
		t.Fatal("the store is not the in-memory one, so this case is not looking at the map it means to")
	}
	ctx := context.Background()

	// Plant entries that expired an hour ago. Planting them directly makes the
	// case deterministic: it is about what later writes do with them, not
	// about waiting for a clock.
	const planted = 1000
	expired := time.Now().Add(-time.Hour)
	for i := range planted {
		s.entries[fmt.Sprintf("dead-%d", i)] = entry{value: []byte("v"), expires: expired}
	}

	// Writes drive the sweep, and each sweeps a random sample of the map, so a
	// batch of them covers the whole of it many times over.
	const written = 500
	for i := range written {
		if err := s.Set(ctx, fmt.Sprintf("live-%d", i), []byte("v"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	s.mu.Lock()
	held := len(s.entries)
	s.mu.Unlock()
	if held > written+200 {
		t.Fatalf("the map holds %d entries after %d live writes and %d long-expired ones: nothing "+
			"sweeps the expired ones out, so the map grows with every key the process has ever written",
			held, written, planted)
	}
}
