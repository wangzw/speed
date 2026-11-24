package static

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

// TestAPasswordIsRememberedFromTheDerivationAndNotFromItsHits holds that hits do not extend the remembered lifetime.
func TestAPasswordIsRememberedFromTheDerivationAndNotFromItsHits(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	clk := newClock()
	b.cache = password.NewCache(16, clk.now)
	derived := counting(b)

	check := func(step string, want int64) {
		t.Helper()
		if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
			t.Fatalf("%s: the right password reads %v (%v)", step, outcome(got), cause(got))
		}
		if derived.Load() != want {
			t.Fatalf("%s: %d derivations, want %d", step, derived.Load(), want)
		}
	}

	check("the first check", 1)
	clk.advance(40 * time.Second)
	check("40s after the derivation, inside the 60s lifetime", 1)
	clk.advance(40 * time.Second)
	check("80s after the derivation, past the lifetime, though a hit came 40s ago", 2)
	clk.advance(40 * time.Second)
	check("40s after the second derivation", 2)
}
