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

// TestTheOutboundClientIsWrappedForTheTrace holds that the product's egress client continues the trace across the provider hop.
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
