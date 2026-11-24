package otel

import (
	"context"
	"runtime"
	"testing"
)

// TestAnUndoneJoinLeavesNothingOnTheCutoff pins that undoing a join takes
// its registration off the cutoff.
func TestAnUndoneJoinLeavesNothingOnTheCutoff(t *testing.T) {
	const joins = 100_000
	c := newCutoff()
	defer c.end()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range joins {
		_, undo := c.join(context.Background())
		undo()
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(c)

	// A registration left behind holds the joined context and the callback, a
	// few hundred bytes each, so the joins above would leave tens of megabytes;
	// a megabyte is room for the runtime's own noise.
	if after.HeapAlloc > before.HeapAlloc+1<<20 {
		t.Errorf("the heap grew by %d bytes over %d undone joins, want no growth: an undone join left its "+
			"registration on the cutoff", after.HeapAlloc-before.HeapAlloc, joins)
	}
}
