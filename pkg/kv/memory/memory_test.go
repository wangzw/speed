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

// TestConforms runs the shared contract.
func TestConforms(t *testing.T) {
	kvsuite.AssertConforms(t, newStore)
}

// TestTheSuiteTellsASlowStoreFromAWrongOne checks that the store passes the shared suite under added
// latency.
func TestTheSuiteTellsASlowStoreFromAWrongOne(t *testing.T) {
	kvsuite.AssertUnderLatency(t, newStore)
}

// TestStanceIsAutoWhenOneReplicaSharesTheState checks that a single replica yields StateAuto.
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

// TestStanceFailsTheStartupWhenSeveralReplicasShareTheState pins the deployment precondition.
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

// TestExpiredEntriesAreSweptOutOfTheMap checks that writing live entries sweeps the long-expired ones
// out of the map.
func TestExpiredEntriesAreSweptOutOfTheMap(t *testing.T) {
	s, ok := newStore(t).(*store)
	if !ok {
		t.Fatal("the store is not the in-memory one, so this case is not looking at the map it means to")
	}
	ctx := context.Background()

	const planted = 1000
	expired := time.Now().Add(-time.Hour)
	for i := range planted {
		s.entries[fmt.Sprintf("dead-%d", i)] = entry{value: []byte("v"), expires: expired}
	}

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
