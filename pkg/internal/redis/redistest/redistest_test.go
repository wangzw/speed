//go:build integration

package redistest

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestConcurrentClaimsNeverHandTwoCallersOneDatabase pins the arbitration the
// index allocation rests on.
//
// The claims come from separate clients under separate owners, which is what
// two test binaries on one machine are; what matters is that the caller's
// identity plays no part in which index it is given. An allocation derived
// from the caller — a counter seeded from the process id, say — hands two
// binaries one database as soon as their identities line up, and neither
// notices until a case in one of them fails on a key the other cleared.
func TestConcurrentClaimsNeverHandTwoCallersOneDatabase(t *testing.T) {
	requireServer(t)
	addr := address()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fewer than the databases on offer, so the claims below do not shut out
	// whatever else is running against the same server.
	const callers = 8
	got := make([]int, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			got[i], errs[i] = claimFor(ctx, addr, "owner-"+strconv.Itoa(i))
		})
	}
	wg.Wait()

	seen := make(map[int]bool, callers)
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if got[i] < 0 {
			t.Fatalf("claim %d was given no database although only %d of them were asked for",
				i, callers)
		}
		if seen[got[i]] {
			t.Fatalf("database %d was handed to two callers at once", got[i])
		}
		seen[got[i]] = true
	}

	// Given back, so this case leaves the server as it found it.
	client := redis.NewClient(&redis.Options{Addr: addr, DB: leaseDB})
	defer func() { _ = client.Close() }()
	for i := range callers {
		if err := client.Del(ctx, leaseKey(got[i])).Err(); err != nil {
			t.Fatalf("releasing database %d: %v", got[i], err)
		}
	}
}
