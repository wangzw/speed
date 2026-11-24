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

// TestConcurrentClaimsNeverHandTwoCallersOneDatabase pins the arbitration the index allocation
// rests on.
func TestConcurrentClaimsNeverHandTwoCallersOneDatabase(t *testing.T) {
	requireServer(t)
	addr := address()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	client := redis.NewClient(&redis.Options{Addr: addr, DB: leaseDB})
	defer func() { _ = client.Close() }()
	for i := range callers {
		if err := client.Del(ctx, leaseKey(got[i])).Err(); err != nil {
			t.Fatalf("releasing database %d: %v", got[i], err)
		}
	}
}
