package memory

import (
	"context"
	"testing"

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

func TestStanceIsAutoWhenOneReplicaSharesTheState(t *testing.T) {
	got := stance(&Config{Replicas: 1})
	if got.State != core.StateAuto {
		t.Fatalf("stance with one replica = %v, want StateAuto: this implementation is the "+
			"capability's zero-configuration default and has to yield to an explicitly enabled backend",
			got.State)
	}
}

func TestStanceStandsDownWhenSeveralReplicasShareTheState(t *testing.T) {
	got := stance(&Config{Replicas: 3})
	if got.State != core.StateDisabled {
		t.Fatalf("stance with three replicas = %v, want StateDisabled", got.State)
	}
	if got.Reason == "" {
		t.Fatal("the stand-down carries no reason, and the reason reaches the startup diagnostics, " +
			"which is the only place a missing capability is noticed")
	}
}
