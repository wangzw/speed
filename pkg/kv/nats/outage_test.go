//go:build integration

package nats

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/internal/cutproxy"
	"github.com/vislake/speed/pkg/internal/natstest"
)

// flushBudget bounds the round trip that drains the client's outbound queue.
// A client with nothing left to send answers at once; one that has stopped
// answering fails the case rather than hanging it.
const flushBudget = 10 * time.Second

// TestAWriteTheCutLinkRefusesDoesNotLandLater is the case for what a failed
// write means here: the capability hands the caller its own failure, and the
// caller decides whether to retry by whether the operation is idempotent
// (design-kv). A write kept by the client library and sent once the link is
// back breaks that decision — the caller was told the write failed and can
// retry it, or leave the entry alone, and either choice is wrong if the store
// writes it anyway. For a counter driven by a read-modify-write that is a
// count taken twice.
//
// The case cuts the link, waits until the client knows it is down, writes
// there, restores the link, flushes the client, and then reads the key: it
// must be absent.
//
// The flush is what makes the read judge "nothing was resent" after the client
// has no work left: it drives whatever the client is holding through a round
// trip to the server, and the outbound queue must then be empty. A fixed pause
// would instead let a client that is slow to send what it kept pass the case
// by landing the write after the read.
func TestAWriteTheCutLinkRefusesDoesNotLandLater(t *testing.T) {
	proxy := cutproxy.Start(t, hostPort(natstest.URL(t)))
	opened, err := open(context.Background(), &Config{
		URL: "nats://" + proxy.Addr(), Bucket: bucketName(), Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	const key = "cut-key"
	// Warm up through the proxy: the bucket is there, and the case has a
	// working link to cut.
	if warmErr := s.Set(context.Background(), "warmup", []byte("v"), 0); warmErr != nil {
		t.Fatalf("the warm-up write: %v", warmErr)
	}

	// The write below has to be issued while the client knows the link is
	// down: that is the state in which the client library would otherwise hold
	// the write in its reconnect buffer and send it once the server answered
	// again.
	proxy.Cut()
	cutproxy.Await(t, 30*time.Second, "the client to notice the cut link", s.nc.IsReconnecting)

	// The deadline is shorter than the library's wait between reconnect
	// attempts (two seconds), so the write is decided while the link is still
	// cut.
	refused, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if writeErr := s.Set(refused, key, []byte("landed"), 0); writeErr == nil {
		t.Fatalf("a write issued while the link was cut succeeded")
	}

	proxy.Restore()
	cutproxy.Await(t, 30*time.Second, "the connection to come back", s.nc.IsConnected)
	// With the link back, flush the client and confirm it has nothing left to
	// send before the read: the flush carries everything the client held
	// through a round trip, so a kept write would have reached the server by
	// now, and the empty queue afterwards is the observation that no write is
	// still waiting to go out.
	if flushErr := s.nc.FlushTimeout(flushBudget); flushErr != nil {
		t.Fatalf("flushing the client before the read: %v", flushErr)
	}
	if pending, bufferedErr := s.nc.Buffered(); bufferedErr != nil {
		t.Fatalf("reading the client's outbound queue: %v", bufferedErr)
	} else if pending != 0 {
		t.Fatalf("the client still has %d bytes to send after a flush: the refused write was kept", pending)
	}
	value, present, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("reading back the refused key: %v", err)
	}
	if present {
		t.Fatalf("the key holds %q after a write this store refused: the refused write was kept and sent "+
			"once the link was back", value)
	}
}
