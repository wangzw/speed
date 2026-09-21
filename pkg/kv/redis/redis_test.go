package redis

import (
	"context"
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
	speedredis "github.com/vislake/speed/pkg/redis"
	"github.com/vislake/speed/pkg/redis/redistest"
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

func TestStanceStandsDownWithoutAServer(t *testing.T) {
	cfg := speedredis.Defaults()
	got := stance(&cfg)
	if got.State != core.StateDisabled {
		t.Fatalf("stance with no address = %v, want StateDisabled", got.State)
	}
	if got.Reason == "" {
		t.Fatal("the stand-down carries no reason")
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	cfg := speedredis.Defaults()
	cfg.Addr = "127.0.0.1:6379"
	got := stance(&cfg)
	if got.State != core.StateEnabled {
		t.Fatalf("stance with an address = %v, want StateEnabled: two configured backends have to "+
			"collide at startup rather than one winning silently", got.State)
	}
}

// TestOnlyTheServersNonNumericWordingBecomesTheSentinel covers the one mapping
// this backend has to make.
//
// It is drawn this narrowly on purpose. The server reports a value that is not
// a number as a generic error with fixed wording, and nothing about it is
// structural, so the match is on that wording — and everything else has to pass
// through untouched, or a caller would read a connection failure as a bad
// value and stop retrying.
func TestOnlyTheServersNonNumericWordingBecomesTheSentinel(t *testing.T) {
	if got := asNotNumeric(nil); got != nil {
		t.Fatalf("asNotNumeric(nil) = %v, want nil", got)
	}
	conn := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	if got := asNotNumeric(conn); !errors.Is(got, conn) {
		t.Fatalf("a connection failure was rewritten as %v", got)
	}
	other := errors.New("ERR unknown command")
	if got := asNotNumeric(other); !errors.Is(got, other) {
		t.Fatalf("an unrelated server error was rewritten as %v", got)
	}
	nonNumeric := errors.New("ERR value is not a valid float")
	if got := asNotNumeric(nonNumeric); !errors.Is(got, kv.ErrNotNumeric) {
		t.Fatalf("the server's non-numeric complaint was not mapped onto the sentinel: %v", got)
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
