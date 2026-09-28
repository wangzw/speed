package nats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/natstest"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/expiry"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
)

// bucketName returns a name no earlier run can have used.
//
// A counter is not enough: the server keeps a bucket between runs, and make
// check runs the tests twice, so the second leg would start with the first
// leg's entries still in the bucket of the same name — which shows up as a
// counter that is already at its expected value.
func bucketName() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("speedtest_%d", time.Now().UnixNano())
	}
	return "speedtest_" + hex.EncodeToString(raw[:])
}

func newStore(t *testing.T) *store {
	t.Helper()
	url := natstest.URL(t)
	bucket := bucketName()
	opened, err := open(context.Background(), &Config{URL: url, Bucket: bucket, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// TestConforms runs the shared contract against a real server.
//
// Each subtest gets a bucket of its own rather than a flushed server: JetStream
// has no logical databases and emptying a bucket is a delete per key, so a
// fresh bucket is both the simplest and the exact expression of "a store with no
// relation to the ones before it".
func TestConforms(t *testing.T) {
	kvsuite.AssertConforms(t, func(t *testing.T) kv.KV {
		return newStore(t)
	})
}

// TestStanceFailsTheStartupWithoutAServer pins the rule that a module named in
// the command tree has to start: this one reaches NATS at a server, so being
// named without one stops the startup and names the input it stopped on. Under
// the stand-down it replaces, the process came up with the capability silently
// missing.
func TestStanceFailsTheStartupWithoutAServer(t *testing.T) {
	_, err := stance(&Config{})
	if err == nil {
		t.Fatal("naming this implementation without a server started anyway")
	}
	if want := ConfigNamespace + "." + urlKey; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

// TestStanceTreatsABlankServerAsNone covers the case an equality check on the
// raw setting misses: a URL that is only whitespace is not a server, and the
// stance is what turns it into a startup failure naming the input rather than a
// connect refusal at the first operation.
func TestStanceTreatsABlankServerAsNone(t *testing.T) {
	for _, url := range []string{"", " ", "\t", "\n"} {
		_, err := stance(&Config{URL: url})
		if err == nil {
			t.Errorf("stance with url %q started anyway", url)
			continue
		}
		if want := ConfigNamespace + "." + urlKey; !strings.Contains(err.Error(), want) {
			t.Errorf("the error for url %q does not name %q: %v", url, want, err)
		}
	}
}

// TestTheServerURLIsSensitive covers the credentials a NATS URL carries. The
// client reads a user and password or a token from it (nats://user:secret@host),
// and an item not marked sensitive is printed by the help output and the
// diagnostics as it was given.
func TestTheServerURLIsSensitive(t *testing.T) {
	var schema config.Schema
	for _, resource := range Module().Resources {
		if s, ok := resource.(config.Schema); ok {
			schema = s
		}
	}
	item, ok := schema.Items[urlKey]
	if !ok {
		t.Fatalf("the module declares no %q item", urlKey)
	}
	if !item.Sensitive {
		t.Fatalf("%s.%s is not marked sensitive, and a URL carries the credentials to connect with",
			ConfigNamespace, urlKey)
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	got, err := stance(&Config{URL: "nats://127.0.0.1:4222"})
	if err != nil {
		t.Fatalf("stance with a server: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a server = %v, want StateEnabled", got.State)
	}
}

// TestTheExpiryTravelsInsideTheValue pins the design this backend forced: a
// bucket's time-to-live is one setting for the whole bucket, so the expiry is
// kept in the value and this package is what decides a key has aged out.
func TestTheExpiryTravelsInsideTheValue(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const ttl = 400 * time.Millisecond
	if err := s.Set(ctx, "k", []byte("v"), ttl); err != nil {
		t.Fatalf("Set: %v", err)
	}
	entry, err := s.bucket.Get(ctx, encodeKey("k"))
	if err != nil {
		t.Fatalf("reading the raw entry: %v", err)
	}
	value, expires, live := expiry.UnwrapFull(entry.Value())
	if !live {
		t.Fatal("a key set with a moment to live reads as expired")
	}
	if expires.IsZero() {
		t.Fatal("the expiry is not in the value, so nothing here can keep one per key")
	}
	if string(value) != "v" {
		t.Fatalf("the value behind the header is %q, want %q", value, "v")
	}
	// Past the expiry, the entry is still in the bucket: it is this package
	// that reports the key as absent, which is what makes a per-key expiry
	// possible at all.
	time.Sleep(ttl + 100*time.Millisecond)
	if _, present, err := s.Get(ctx, "k"); err != nil || present {
		t.Fatalf("Get past the ttl: present=%v err=%v", present, err)
	}
	if _, err := s.bucket.Get(ctx, encodeKey("k")); err != nil {
		t.Fatalf("the entry itself is gone, so this case no longer shows what it means to: %v", err)
	}
}

// TestALongKeyLeavesTheConnectionUsable covers the failure a long key used to
// cause here, which reached far past the key itself. A key's spelling is part
// of the subject every request carries, and a subject past the server's control
// line limit makes the server drop the client: the connection closes for good,
// and every later operation on every key fails until the process restarts. The
// case writes a key well past that limit and then uses a short one on the same
// connection.
func TestALongKeyLeavesTheConnectionUsable(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	long := strings.Repeat("x", 8000)
	if err := s.Set(ctx, long, []byte("v"), 0); err != nil {
		t.Fatalf("Set on an 8000-byte key: %v", err)
	}
	if err := s.Set(ctx, "short", []byte("w"), 0); err != nil {
		t.Fatalf("Set on a short key after a long one: %v", err)
	}
	if got, present, err := s.Get(ctx, "short"); err != nil || !present || string(got) != "w" {
		t.Fatalf("Get on a short key after a long one: %q, present=%v, err=%v", got, present, err)
	}
}

// TestTheConnectionKeepsReconnectingThroughAnOutage covers a server that is
// away longer than the client's reconnect budget. Past that budget — sixty
// attempts two seconds apart, by the client library's default — the client
// closes the connection for good, and every operation fails until the process
// restarts, although the server came back long ago. The other remote backends
// recover on their own once their server answers again, and so must this one:
// the connection has to keep trying for as long as the store is open.
//
// The case reads the budget the connection was built with rather than waiting
// out an outage of minutes.
func TestTheConnectionKeepsReconnectingThroughAnOutage(t *testing.T) {
	s := newStore(t)
	if got := s.nc.Opts.MaxReconnect; got >= 0 {
		t.Fatalf("the connection gives up after %d reconnect attempts, and stays closed after an outage "+
			"that outlasts them", got)
	}
}

// TestTwoStoresOnOneBucketShareTheSameEntries is what this backend is for.
func TestTwoStoresOnOneBucketShareTheSameEntries(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	bucket := bucketName()
	mk := func() *store {
		opened, err := open(ctx, &Config{URL: url, Bucket: bucket, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		s := opened.(*store)
		t.Cleanup(func() { _ = s.Close(ctx) })
		return s
	}
	first, second := mk(), mk()

	if err := first.Set(ctx, "k", []byte("from-the-first"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, present, err := second.Get(ctx, "k")
	if err != nil || !present {
		t.Fatalf("Get on the other store: present=%v err=%v", present, err)
	}
	if string(got) != "from-the-first" {
		t.Fatalf("the other store read %q", got)
	}

	if _, incrErr := first.IncrByFloat(ctx, "n", 5); incrErr != nil {
		t.Fatalf("IncrByFloat: %v", incrErr)
	}
	if _, incrErr := second.IncrByFloat(ctx, "n", 7); incrErr != nil {
		t.Fatalf("IncrByFloat: %v", incrErr)
	}
	text, _, err := first.Get(ctx, "n")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	total, err := strconv.ParseFloat(string(text), 64)
	if err != nil {
		t.Fatalf("the counter holds %q, which is not a number", text)
	}
	if total != 12 {
		t.Fatalf("the two stores counted to %v between them, want 12", total)
	}
}

// TestCompareAndSwapOnAnExpiredKeyUnderContention pins the expired path's
// sentinel handling: two swaps that both read the same expired revision are not
// a failure for the one that loses. It is a swap that did not happen — the same
// reading the live path already gives a contended key — and a caller that got
// an error here could not tell it from the store being broken.
func TestCompareAndSwapOnAnExpiredKeyUnderContention(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const key = "contested"

	// An entry whose time has passed but which is still in the bucket: this
	// backend decides expiry itself, so the row is there to be contended.
	if err := s.Set(ctx, key, []byte("v"), 50*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	const writers = 8
	type outcome struct {
		swapped bool
		err     error
	}
	results := make([]outcome, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			swapped, err := s.CompareAndSwap(ctx, key, nil, []byte("next"))
			results[i] = outcome{swapped, err}
		}(i)
	}
	close(start)
	wg.Wait()

	swapped := 0
	for i, r := range results {
		if r.err != nil {
			t.Errorf("writer %d reported %v; a lost revision is a swap that did not happen, not a failure", i, r.err)
		}
		if r.swapped {
			swapped++
		}
	}
	if swapped != 1 {
		t.Fatalf("%d writers report a successful swap, want exactly one", swapped)
	}
}
