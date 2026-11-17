//go:build integration

package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/event"
	speedredis "github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
)

// TestPublishDoesNotWaitOnBlockedReaders pins what a publish costs when the
// bus reads more types than the connection pool holds connections. Every
// reader blocks in its read for up to readBlock, holding a connection the
// whole time; readers drawn from the pool the publishes use would leave a
// publish queued behind them for about a second per reader past the pool's
// size.
func TestPublishDoesNotWaitOnBlockedReaders(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, addr, db, "")
	types := speedredis.Defaults().PoolSize + 6
	for i := range types {
		b.Subscribe(fmt.Sprintf("pool.t%d", i), "test", func(context.Context, event.Event) error { return nil })
	}
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Every reader is in its blocking read by now.
	time.Sleep(300 * time.Millisecond)

	const publishes = 10
	began := time.Now()
	for range publishes {
		if err := b.Publish(ctx, event.Event{Type: "pool.t0"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	// A publish is one round trip to a local server; the bound leaves room for
	// a loaded machine and is still a small fraction of one blocked read. It
	// tells the right implementation from one that waits for a connection, so
	// it carries the caller's scale alone: the race factor slows the program
	// under test, not the difference between one round trip and a blocked
	// reader's read. The wrong implementation needs about one blocked read per
	// publish that waits, so one blocked read is its time, stated to
	// DeclaredAgainst. A twentieth of it for the average of ten publishes is
	// still far below the blocked read and leaves a round trip room to be slow.
	if each := time.Since(began) / publishes; each > coretest.DeclaredAgainst(t, 0, readBlock/20, readBlock) {
		t.Fatalf("a publish took %v on average with %d readers on a pool of %d connections: it "+
			"waited for a connection a blocked reader was holding", each, types, speedredis.Defaults().PoolSize)
	}
}
