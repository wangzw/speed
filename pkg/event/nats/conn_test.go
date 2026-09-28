package nats

import (
	"testing"

	"github.com/vislake/speed/pkg/internal/natstest"
)

// TestTheConnectionNeverStopsReconnecting pins the reconnect budget. The
// library's default gives up after 60 attempts two seconds apart, and past
// that the connection would be closed for good: a broker outage of a little
// over two minutes would leave every publish and every fetch failing until the
// process restarted, with the broker long back.
func TestTheConnectionNeverStopsReconnecting(t *testing.T) {
	b := openBus(t, natstest.URL(t), "")
	if limit := b.nc.Opts.MaxReconnect; limit >= 0 {
		t.Fatalf("the connection gives up after %d reconnect attempts; a bus lives as long as its "+
			"process and has to reconnect for as long as it takes", limit)
	}
}
