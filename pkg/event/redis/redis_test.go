package redis

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	speedredis "github.com/vislake/speed/pkg/internal/redis"
)

// TestStanceFailsWithoutAServer pins the enablement rule at this
// backend's own precondition.
func TestStanceFailsWithoutAServer(t *testing.T) {
	cfg := Config{Config: speedredis.Defaults()}
	_, err := stance(&cfg)
	if err == nil {
		t.Fatal("stance with no address did not fail the startup")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+speedredis.AddrKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
}

// TestStanceEnablesOnceAServerIsNamed checks that a named server states
// StateEnabled.
func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	cfg := Config{Config: speedredis.Defaults()}
	cfg.Addr = "127.0.0.1:6379"
	got, err := stance(&cfg)
	if err != nil {
		t.Fatalf("stance with an address failed the startup: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with an address = %v, want StateEnabled", got.State)
	}
}

// TestStreamsAreNamespaced keeps this implementation out of everything else on
// a server it may be sharing.
func TestStreamsAreNamespaced(t *testing.T) {
	if got := streamKey("order.placed"); !strings.HasPrefix(got, streamPrefix) {
		t.Fatalf("streamKey returned %q, which is not under %q", got, streamPrefix)
	}
}
