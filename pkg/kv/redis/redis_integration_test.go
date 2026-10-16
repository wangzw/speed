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

// newStore hands out one store over a real server, cleared before each subtest.
//
// The contract's factory promises a store with no relation to the ones before
// it, and a single database index plus a flush is how this backend keeps that
// promise without paying for a container per case.
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

// TestAPaddedAddressStillReachesTheServer is the half the stance cannot hold on
// its own. The stance reads a trimmed address, so one written with surrounding
// whitespace passes it; the client is built from that same value and the server
// answers. Handing the client the text as written would refuse at the dial an
// address the stance had just accepted, and the failure would land in New
// rather than in Prepare.
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

// What this backend does to an expiry — the compare-and-swap carries it over
// with KEEPTTL rather than clearing it, and an increment of an existing key
// leaves it where it was — is covered by the contract suite, which asserts both
// against a real server here. A case repeating them would be a second copy of
// one property, free to rot on its own.

// TestKeysLiveUnderTheImplementationPrefix pins the namespace boundary between
// this backend and everything else that may share its server. The event
// module's Redis backend can be pointed at the same database, and without a
// prefix of its own, a key here that reads like one of the event streams would
// let a Set replace the stream — the events in it gone with no trace.
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

	// A key spelled exactly like one of the event streams, to make the
	// collision this guards against the one under test.
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

// libraryLog records what the client library prints through its own logger.
type libraryLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *libraryLog) Printf(_ context.Context, format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

// stderrLog is the client library's own default destination, put back once a
// case that captured the library's output is done.
type stderrLog struct{ log *log.Logger }

func (l stderrLog) Printf(_ context.Context, format string, v ...any) { l.log.Printf(format, v...) }

// TestASubMillisecondTTLPrintsNothing covers the client library's own logger,
// which writes to the standard error, outside the logging an assembly sets up:
// handed a ttl under a millisecond, the library prints a warning and rounds it
// up to one. The store rounds such a ttl itself before the library sees it, so
// nothing is printed, and the key still ages out.
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
