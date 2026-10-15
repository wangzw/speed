package redis

import (
	"context"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
)

// inspect is a client of the case's own, for reading the server after the bus
// has closed its clients.
func inspect(t *testing.T, addr string, db int) *goredis.Client {
	t.Helper()
	c := goredis.NewClient(&goredis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// groupNames lists the consumer groups a stream carries.
func groupNames(t *testing.T, c *goredis.Client, stream string) map[string][]string {
	t.Helper()
	groups, err := c.XInfoGroups(context.Background(), stream).Result()
	if err != nil {
		t.Fatalf("reading the stream's groups: %v", err)
	}
	out := make(map[string][]string, len(groups))
	for _, g := range groups {
		consumers, err := c.XInfoConsumers(context.Background(), stream, g.Name).Result()
		if err != nil {
			t.Fatalf("reading the consumers of %s: %v", g.Name, err)
		}
		for _, consumer := range consumers {
			out[g.Name] = append(out[g.Name], consumer.Name)
		}
		if out[g.Name] == nil {
			out[g.Name] = []string{}
		}
	}
	return out
}

// startReading subscribes to one type, starts the bus, and publishes once, so
// the reader has registered itself as a consumer of its group.
func startReading(t *testing.T, b *bus, eventType string) {
	t.Helper()
	ctx := context.Background()
	got := make(chan struct{}, 1)
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		got <- struct{}{}
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	<-got
}

// TestClosingRemovesTheProcesssOwnGroup pins what a process leaves on the
// server once it is gone. A group named after the process is read by that
// process alone, so one left behind is never read again, and every start —
// each replica of a rolling deploy, each command that takes the bus in — would
// add one more to every stream it subscribes to, each showing a lag that only
// grows.
func TestClosingRemovesTheProcesssOwnGroup(t *testing.T) {
	addr, db := redistest.Acquire(t)
	b := openBus(t, addr, db, "")
	startReading(t, b, "t")
	if _, ok := groupNames(t, inspect(t, addr, db), streamKey("t"))[b.group]; !ok {
		t.Fatalf("the bus's own group %s is not on the stream while it reads", b.group)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if consumers, ok := groupNames(t, inspect(t, addr, db), streamKey("t"))[b.group]; ok {
		t.Fatalf("the group %s, named after a process that has closed, is still on the stream "+
			"with the consumers %v", b.group, consumers)
	}
}

// TestClosingKeepsAConfiguredGroup is the other side: a group the host named
// is shared with the other replicas, or outlives this process on purpose, so
// closing takes only this process's consumer out of it.
func TestClosingKeepsAConfiguredGroup(t *testing.T) {
	addr, db := redistest.Acquire(t)
	const group = "configured-group"
	b := openBus(t, addr, db, group)
	startReading(t, b, "t")
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	consumers, ok := groupNames(t, inspect(t, addr, db), streamKey("t"))[group]
	if !ok {
		t.Fatalf("the configured group %s was removed when one of its replicas closed", group)
	}
	for _, name := range consumers {
		if name == b.consumer {
			t.Fatalf("the closed process's consumer %s is still in the configured group", b.consumer)
		}
	}
}
