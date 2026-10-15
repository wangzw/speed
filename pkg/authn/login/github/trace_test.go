package github

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// TestTheOutboundClientIsWrappedForTheTrace holds that the restricted egress
// client the product assembles is wrapped with the trace transport, which is
// what makes an outbound call continue the trace across the hop to the identity
// provider: without the wrap the token exchange and the two API reads reach the
// provider with no trace context and the trace breaks at this hop, which no
// layer reports.
//
// The product is taken from a real registry driven to the stage where the
// instance exists, so the assertion is on what the module's own New assembles
// rather than on a client a case wrapped by hand: a case that built its own
// client and wrapped it the same way would pass with the wrap removed from the
// descriptor's New, holding nothing about this module.
//
// The wrap is judged structurally rather than by driving a call because the
// production client speaks https only, to a public address, with the system
// roots: a call to a test provider serving its own certificate cannot get past
// the TLS handshake, and the header the call would carry never leaves the
// process. What the wrap is the only thing that changes is the concrete type of
// the transport the product holds, so it is compared against the one Transport
// returns.
func TestTheOutboundClientIsWrappedForTheTrace(t *testing.T) {
	wrapped := reflect.TypeOf(observability.Transport(nil))
	reg := registryWithReader(t, completeBody())
	reg.Register(Module())
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			methods := core.ResolveAll[authn.LoginMethod](reg)
			if len(methods) != 1 {
				return fmt.Errorf("the drive delivered %d login methods, want this module's one", len(methods))
			}
			m, ok := methods[0].(*method)
			if !ok {
				return fmt.Errorf("the delivered method is %T, not this module's product", methods[0])
			}
			if got := reflect.TypeOf(m.client.Transport); got != wrapped {
				t.Errorf("the restricted egress client's transport is %v, want %v: an outbound call would break "+
					"the trace across the hop to the identity provider", got, wrapped)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the registry to the instance: %v", err)
	}
}
