package password

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func heapObjects() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// TestDerivationsOfLargeEntriesNeverHoldMoreThanTheBudget keeps the heap within
// the stated budget plus a fixed overhead while 64 MiB derivations run.
func TestDerivationsOfLargeEntriesNeverHoldMoreThanTheBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates what the entries state")
	}
	const (
		arriving    = 12
		entryKiB    = 64 * 1024
		budgetKiB   = 128 * 1024
		overheadMiB = 48
	)
	e := parsed(t, Params{Algorithm: Argon2id, MemoryKiB: entryKiB, Iterations: 1, Parallelism: 1}, "secret")
	g := NewGovernor(arriving, budgetKiB)

	defer debug.SetGCPercent(debug.SetGCPercent(5))
	runtime.GC()
	baseline := heapObjects()

	var peak uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		for {
			if used := heapObjects(); used > peak {
				peak = used
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()

	var mu sync.Mutex
	var passed, busy int
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range arriving {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := g.Verify(context.Background(), e, "secret", 0)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, authn.ErrBusy):
				busy++
			case err == nil && ok:
				passed++
			default:
				t.Errorf("a derivation gave %v, %v, want a match or busy", ok, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(stop)
	<-sampled

	if busy == 0 {
		t.Errorf("no derivation was refused as busy among %d that arrived together against room for two", arriving)
	}
	if passed == 0 || passed > arriving-busy {
		t.Errorf("%d derivations passed and %d were busy of %d, want some that ran and none unaccounted for", passed, busy, arriving)
	}
	var grown uint64
	if peak > baseline {
		grown = peak - baseline
	}
	if limit := uint64(budgetKiB)*1024 + overheadMiB<<20; grown > limit {
		t.Errorf("the heap grew by %d MiB while the derivations ran, want at most %d MiB: the budget and a fixed overhead",
			grown>>20, limit>>20)
	}
}
