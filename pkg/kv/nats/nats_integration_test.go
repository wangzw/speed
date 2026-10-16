//go:build integration

package nats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

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
	// Just past the expiry, the entry is still in the bucket — the server
	// removes it only a margin later: it is this package that reports the key
	// as absent, which is what makes a per-key expiry possible at all.
	time.Sleep(ttl + 100*time.Millisecond)
	if _, present, err := s.Get(ctx, "k"); err != nil || present {
		t.Fatalf("Get past the ttl: present=%v err=%v", present, err)
	}
	if _, err := s.bucket.Get(ctx, encodeKey("k")); err != nil {
		t.Fatalf("the entry itself is gone, so this case no longer shows what it means to: %v", err)
	}
}

// TestALongKeyLeavesTheConnectionUsable covers a failure a long key can cause
// that reaches far past the key itself. A key's spelling is part of the subject
// every request carries, and a subject past the server's control line limit
// makes the server drop the client: the connection closes for good, and every
// later operation on every key fails until the process restarts. The case
// writes a key well past that limit and then uses a short one on the same
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

// TestAnExistingBucketKeepsItsConfiguration covers a bucket an operator
// provisioned: its history, its size limits, its description, and on a cluster
// its replica count. Opening binds to it as it is. Creating or updating it to a
// configuration naming only the bucket would put every one of those back to
// the default, on every replica's every start — dropping the high availability
// the operator configured, with no sign anywhere that it had.
func TestAnExistingBucketKeepsItsConfiguration(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	bucket := bucketName()
	want := jetstream.KeyValueConfig{
		Bucket:       bucket,
		Description:  "provisioned by the operator",
		History:      5,
		MaxBytes:     10 << 20,
		MaxValueSize: 4096,
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("opening JetStream: %v", err)
	}
	if _, createErr := js.CreateKeyValue(ctx, want); createErr != nil {
		t.Fatalf("provisioning the bucket: %v", createErr)
	}

	opened, err := open(ctx, &Config{URL: url, Bucket: bucket, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("open over a provisioned bucket: %v", err)
	}
	t.Cleanup(func() { _ = opened.(*store).Close(ctx) })

	kv, err := js.KeyValue(ctx, bucket)
	if err != nil {
		t.Fatalf("binding to the bucket: %v", err)
	}
	status, err := kv.Status(ctx)
	if err != nil {
		t.Fatalf("reading the bucket's status: %v", err)
	}
	got := status.(*jetstream.KeyValueBucketStatus).Config()
	if got.Description != want.Description || got.History != want.History ||
		got.MaxBytes != want.MaxBytes || got.MaxValueSize != want.MaxValueSize {
		t.Fatalf("opening rewrote the bucket's configuration: description %q, history %d, max bytes %d, "+
			"max value size %d; the operator set %q, %d, %d, %d", got.Description, got.History, got.MaxBytes,
			got.MaxValueSize, want.Description, want.History, want.MaxBytes, want.MaxValueSize)
	}
}

// TestABucketWithoutMessageTTLTakesExpiringWrites covers a bucket this
// implementation finds rather than creates — provisioned by an operator, or
// created with nothing but its name. Its stream refuses a message carrying a
// time-to-live, and every write with an expiry gives its message one so the
// server can remove the entry once it has expired; opening has to switch the
// setting on, or every such write would fail.
func TestABucketWithoutMessageTTLTakesExpiringWrites(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	bucket := bucketName()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("opening JetStream: %v", err)
	}
	if _, createErr := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: bucket}); createErr != nil {
		t.Fatalf("provisioning the bucket: %v", createErr)
	}

	opened, err := open(ctx, &Config{URL: url, Bucket: bucket, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("open over a bucket without a per-message time-to-live: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(ctx) })
	if err := s.Set(ctx, "cached", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set with a ttl: %v", err)
	}
	if _, err := s.IncrByFloatWithTTL(ctx, "window", 1, time.Minute); err != nil {
		t.Fatalf("IncrByFloatWithTTL: %v", err)
	}
	if err := s.Delete(ctx, "cached"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// TestTheTimeoutBoundsAnOperation pins what the timeout item says it does: it
// bounds connecting and one operation. An operation whose context carries no
// deadline of its own is bounded by it, so a server that stops answering fails
// the operation after the configured time, not after the client library's own
// default of five seconds.
func TestTheTimeoutBoundsAnOperation(t *testing.T) {
	proxy := startStallingProxy(t, hostPort(natstest.URL(t)))
	const timeout = 500 * time.Millisecond
	opened, err := open(context.Background(), &Config{URL: proxy.url(), Bucket: bucketName(), Timeout: timeout})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	proxy.stalled.Store(true)
	started := time.Now()
	_, _, err = s.Get(context.Background(), "k")
	elapsed := time.Since(started)
	proxy.stalled.Store(false)
	if err == nil {
		t.Fatal("a Get the server never answered succeeded")
	}
	if elapsed > 4*timeout {
		t.Fatalf("a Get the server never answered took %s to fail, with a timeout of %s", elapsed, timeout)
	}
}

// TestExpiredAndDeletedEntriesLeaveTheBucket covers the storage a long-running
// deployment accumulates. The expiry this package keeps in front of a value
// hides an expired entry from every read, but the entry itself stays in the
// bucket's stream until something writes its key again — and a rate limiter's
// window keys, an idempotency guard's request keys and a cache's entries are
// never written again once they expire. A delete leaves a marker in the stream
// in the same way. A bucket that kept either would grow with every key ever
// written.
//
// The case writes entries with a moment to live through both writes that
// carry an expiry and deletes a third, then reads the bucket's stream below the
// key-value layer.
func TestExpiredAndDeletedEntriesLeaveTheBucket(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Set(ctx, "cached", []byte("v"), 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := s.IncrByFloatWithTTL(ctx, "window", 1, 10*time.Millisecond); err != nil {
		t.Fatalf("IncrByFloatWithTTL: %v", err)
	}
	if err := s.Set(ctx, "deleted", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete(ctx, "deleted"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	js, err := jetstream.New(s.nc)
	if err != nil {
		t.Fatalf("opening JetStream: %v", err)
	}
	stream, err := js.Stream(ctx, "KV_"+s.bucket.Bucket())
	if err != nil {
		t.Fatalf("binding to the bucket's stream: %v", err)
	}
	subject := func(key string) string { return "$KV." + s.bucket.Bucket() + "." + encodeKey(key) }

	// The delete's marker is what stays in the stream for a deleted key, so it
	// has to carry an expiry of its own.
	marker, err := stream.GetLastMsgForSubject(ctx, subject("deleted"))
	if err != nil {
		t.Fatalf("reading the deleted key's last message: %v", err)
	}
	if marker.Header.Get(jetstream.MsgTTLHeader) == "" {
		t.Errorf("the delete left a marker with no expiry, which the stream keeps for good: headers %v",
			marker.Header)
	}

	// The expired entries leave the stream; a marker the server puts in their
	// place is not an entry, and ages out itself.
	deadline := time.Now().Add(5 * time.Second)
	for _, key := range []string{"cached", "window"} {
		for {
			if _, getErr := s.bucket.Get(ctx, encodeKey(key)); errors.Is(getErr, jetstream.ErrKeyNotFound) {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("the entry for %q is still in the bucket well past its expiry", key)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
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
