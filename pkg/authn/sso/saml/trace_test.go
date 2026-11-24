package saml

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// TestTheOutboundClientIsWrappedForTheTrace checks that the outbound client carries the trace of the call's context.
func TestTheOutboundClientIsWrappedForTheTrace(t *testing.T) {
	wrapped := reflect.TypeOf(observability.Transport(nil))
	idp := newIDP(t)
	providers := newFakeProviders(providerRow(providerConfigJSON(t, idp, nil)))
	reg := buildRegistry(t, providers)
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants(),
		Action: func(_ context.Context, reg *core.Registry) error {
			methods := core.ResolveAll[authn.LoginMethod](reg)
			if len(methods) != 1 {
				return fmt.Errorf("the drive delivered %d login methods, want this module's one", len(methods))
			}
			p, ok := methods[0].(*product)
			if !ok {
				return fmt.Errorf("the delivered method is %T, not this module's product", methods[0])
			}
			if got := reflect.TypeOf(p.client.Transport); got != wrapped {
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
