package nats

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/internal/cutproxy"
	"github.com/vislake/speed/pkg/internal/natstest"
)

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
// there, restores the link, and then reads the key: it must be absent.
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
	// Give a client that kept the write the moment it needs to send what it
	// kept, so a kept write cannot pass this case by landing late.
	time.Sleep(time.Second)
	value, present, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("reading back the refused key: %v", err)
	}
	if present {
		t.Fatalf("the key holds %q after a write this store refused: the refused write was kept and sent "+
			"once the link was back", value)
	}
}
