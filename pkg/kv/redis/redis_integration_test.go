//go:build integration

package redis

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	speedredis "github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
)

func newStore(t *testing.T, s *store) kvsuite.Factory {
	t.Helper()
	ctx := context.Background()
	return func(t *testing.T) kv.KV {
		t.Helper()
		if err := s.client.FlushDB(ctx).Err(); err != nil {
			t.Fatalf("clearing the database: %v", err)
		}
		return s
	}
}

// TestConforms runs the shared key-value contract against the Redis backend.
func TestConforms(t *testing.T) {
	addr, db := redistest.Acquire(t)
	proto := speedredis.Defaults()
	proto.Addr, proto.DB = addr, db
	opened, err := open(context.Background(), &proto)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	kvsuite.AssertConforms(t, newStore(t, s))
}

// TestAPaddedAddressStillReachesTheServer is the half the stance cannot hold on its own.
func TestAPaddedAddressStillReachesTheServer(t *testing.T) {
	addr, db := redistest.Acquire(t)
	proto := speedredis.Defaults()
	proto.Addr, proto.DB = " "+addr+" ", db
	opened, err := open(context.Background(), &proto)
	if err != nil {
		t.Fatalf("opening against a padded address: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if _, found, err := s.Get(context.Background(), "padded"); err != nil || found {
		t.Fatalf("reading through a padded address: found = %v, err = %v", found, err)
	}
}

// TestKeysLiveUnderTheImplementationPrefix pins the namespace boundary between this backend and
// everything else that may share its server.
func TestKeysLiveUnderTheImplementationPrefix(t *testing.T) {
	addr, db := redistest.Acquire(t)
	proto := speedredis.Defaults()
	proto.Addr, proto.DB = addr, db
	opened, err := open(context.Background(), &proto)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	ctx := context.Background()

	const key = "speed:events:order.placed"
	if err := s.Set(ctx, key, []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := s.client.Get(ctx, prefixed(key)).Bytes(); err != nil || string(got) != "v" {
		t.Fatalf("reading the prefixed key gave (%q, %v); the value is not where this backend says it puts it", got, err)
	}
	if _, err := s.client.Get(ctx, key).Bytes(); !errors.Is(err, goredis.Nil) {
		t.Fatalf("the bare key %q holds something after a Set: this backend is not namespacing its keys, and "+
			"a stream living there would have been overwritten", key)
	}
}

type libraryLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *libraryLog) Printf(_ context.Context, format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

type stderrLog struct{ log *log.Logger }

func (l stderrLog) Printf(_ context.Context, format string, v ...any) { l.log.Printf(format, v...) }

// TestASubMillisecondTTLPrintsNothing checks that a TTL below a millisecond makes the client library
// print nothing and the key is already gone.
func TestASubMillisecondTTLPrintsNothing(t *testing.T) {
	addr, db := redistest.Acquire(t)
	proto := speedredis.Defaults()
	proto.Addr, proto.DB = addr, db
	opened, err := open(context.Background(), &proto)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	captured := &libraryLog{}
	goredis.SetLogger(captured)
	t.Cleanup(func() {
		goredis.SetLogger(stderrLog{log: log.New(os.Stderr, "redis: ", log.LstdFlags|log.Lshortfile)})
	})

	ctx := context.Background()
	const ttl = 500 * time.Microsecond
	if err := s.Set(ctx, "set", []byte("v"), ttl); err != nil {
		t.Fatalf("Set: %v", err)
	}
	captured.mu.Lock()
	lines := captured.lines
	captured.mu.Unlock()
	if len(lines) > 0 {
		t.Fatalf("a ttl of %s made the client library print %q", ttl, lines)
	}
	time.Sleep(20 * time.Millisecond)
	if _, present, err := s.Get(ctx, "set"); err != nil || present {
		t.Fatalf("Get past a %s ttl: present=%v err=%v", ttl, present, err)
	}
}
