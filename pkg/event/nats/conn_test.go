//go:build integration

package nats

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/natstest"
	"github.com/vislake/speed/pkg/log"
)

// TestTheConnectionNeverStopsReconnecting pins the reconnect budget.
func TestTheConnectionNeverStopsReconnecting(t *testing.T) {
	b := openBus(t, natstest.URL(t), "")
	if limit := b.nc.Opts.MaxReconnect; limit >= 0 {
		t.Fatalf("the connection gives up after %d reconnect attempts; a bus lives as long as its "+
			"process and has to reconnect for as long as it takes", limit)
	}
}

// TestTheTimeoutBoundsEveryOperation pins what the timeout item bounds.
func TestTheTimeoutBoundsEveryOperation(t *testing.T) {
	const timeout = 1234 * time.Millisecond
	opened, err := open(context.Background(), &Config{URL: natstest.URL(t), Timeout: timeout}, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	b := opened.(*bus)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	if got := b.nc.Opts.Timeout; got != timeout {
		t.Fatalf("connecting is bounded by %v, want the configured %v", got, timeout)
	}
	if got := b.js.Options().DefaultTimeout; got != timeout {
		t.Fatalf("a request to the server is bounded by %v, want the configured %v", got, timeout)
	}
}

// TestATimeoutThatIsNotPositiveTakesTheDefault covers a typo in the item.
func TestATimeoutThatIsNotPositiveTakesTheDefault(t *testing.T) {
	for _, configured := range []time.Duration{0, -time.Second} {
		opened, err := open(context.Background(), &Config{URL: natstest.URL(t), Timeout: configured}, log.Default(), tracing.Noop())
		if err != nil {
			t.Fatalf("open with the timeout %v: %v", configured, err)
		}
		b := opened.(*bus)
		if b.nc.Opts.Timeout != defaultTimeout || b.js.Options().DefaultTimeout != defaultTimeout {
			t.Errorf("the timeout %v gave the connect %v and a request %v, want the default %v",
				configured, b.nc.Opts.Timeout, b.js.Options().DefaultTimeout, defaultTimeout)
		}
		_ = b.Close(context.Background())
	}
}

// TestAConsumerNameTheServerRefusesIsRefusedNamingTheItem pins where a
// durable name JetStream cannot carry is caught.
func TestAConsumerNameTheServerRefusesIsRefusedNamingTheItem(t *testing.T) {
	url := natstest.URL(t)
	for _, name := range []string{"crm.prod", "crm prod", "crm*", "crm>", "crm/prod", `crm\prod`, "crm\tprod"} {
		// The premise: the durable really cannot be created under this name.
		b := openBus(t, url, name)
		b.Subscribe(nextType(), "test", func(context.Context, event.Event) error { return nil })
		if err := b.begin(context.Background()); err == nil {
			t.Errorf("a durable named %q was created, so refusing the name would refuse a usable one", name)
			continue
		}
		_, err := stance(&Config{URL: url, ConsumerName: name, Timeout: defaultTimeout})
		if err == nil {
			t.Errorf("the consumer name %q passed the stance and fails only at the start stage", name)
			continue
		}
		if want := ConfigNamespace + "." + consumerNameKey; !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of %q does not name %s: %v", name, want, err)
		}
	}
}
