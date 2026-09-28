package redis

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
)

// TestAnEntryDeliveredWhileStoppingIsAcknowledged pins what the stop stage
// leaves behind on this backend: nothing pending. An entry a reader took is
// this consumer's alone, and nothing here claims an entry back from a
// consumer, so one left pending at shutdown is delivered by no one — which is
// what a handler still running when the drive's context was cancelled used to
// cause, its acknowledgement made on a context already done.
func TestAnEntryDeliveredWhileStoppingIsAcknowledged(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, addr, db, "")
	entered := make(chan struct{})
	release := make(chan struct{})
	b.Subscribe("slow", func(context.Context, event.Event) error {
		close(entered)
		<-release
		return nil
	})
	drive, cancel := context.WithCancel(ctx)
	if err := b.begin(drive); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "slow"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the subscriber to start")
	}
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- b.Stop(ctx) }()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}

	pending, err := b.client.XPending(ctx, streamKey("slow"), b.group).Result()
	if err != nil {
		t.Fatalf("reading the group's pending entries: %v", err)
	}
	if pending.Count != 0 {
		t.Fatalf("%d entries are still pending in this replica's group after the stop stage: the "+
			"entry delivered while stopping was never acknowledged", pending.Count)
	}
}
