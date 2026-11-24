//go:build integration

package nats

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/internal/cutproxy"
	"github.com/vislake/speed/pkg/internal/natstest"
)

const flushBudget = 10 * time.Second

// TestAWriteTheCutLinkRefusesDoesNotLandLater checks that a write issued while the link is cut fails
// and is not buffered and sent after the link returns.
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
	if warmErr := s.Set(context.Background(), "warmup", []byte("v"), 0); warmErr != nil {
		t.Fatalf("the warm-up write: %v", warmErr)
	}

	proxy.Cut()
	cutproxy.Await(t, 30*time.Second, "the client to notice the cut link", s.nc.IsReconnecting)

	refused, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if writeErr := s.Set(refused, key, []byte("landed"), 0); writeErr == nil {
		t.Fatalf("a write issued while the link was cut succeeded")
	}

	proxy.Restore()
	cutproxy.Await(t, 30*time.Second, "the connection to come back", s.nc.IsConnected)
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
