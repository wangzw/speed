package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

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
	// a loaded machine and is still a small fraction of one blocked read.
	if each := time.Since(began) / publishes; each > readBlock/5 {
		t.Fatalf("a publish took %v on average with %d readers on a pool of %d connections: it "+
			"waited for a connection a blocked reader was holding", each, types, speedredis.Defaults().PoolSize)
	}
}
