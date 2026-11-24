package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// TestTheEndpointListRefusesShapesItCannotRunOn checks that an empty list, the
// every-endpoint name, an empty name and a duplicate are refused naming the key.
func TestTheEndpointListRefusesShapesItCannotRunOn(t *testing.T) {
	cases := map[string]string{
		"empty":      `[]`,
		"star":       `["*"]`,
		"empty name": `[""]`,
		"duplicate":  `["public", "public"]`,
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"settings": {"admin": {"endpoints": ` + endpoints + `}}}`
			err := managementAssembly{body: body}.drive(t, core.StagePrepare, nil)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), endpointsKey) {
				t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
			}
		})
	}
}

// TestTheEndpointItemIsRequiredOnThePrimarySource checks that the endpoint list is required and takes the primary source alone.
func TestTheEndpointItemIsRequiredOnThePrimarySource(t *testing.T) {
	var declared config.Schema
	for _, res := range Module().Resources {
		if s, ok := res.(config.Schema); ok {
			declared = s
		}
	}
	if declared.Namespace != ConfigNamespace {
		t.Fatalf("the schema namespace is %q, want %q", declared.Namespace, ConfigNamespace)
	}
	item, ok := declared.Items[endpointsItem]
	if !ok {
		t.Fatalf("the schema declares no %q item: %v", endpointsItem, declared.Items)
	}
	if !item.Required {
		t.Error("the endpoint list is not required")
	}
	if item.Origins != config.OriginPrimary {
		t.Errorf("the endpoint list takes origins %v, want the primary source alone", item.Origins)
	}
}

// TestTheDescriptorRequiresItsCapabilities checks the required capabilities and the optional logger.
func TestTheDescriptorRequiresItsCapabilities(t *testing.T) {
	optional := map[any]bool{}
	for _, req := range Module().Requires {
		optional[req.Token] = req.Optional
	}
	for _, tk := range []any{
		(*settings.Store)(nil), (*guard.Guard)(nil), (*audit.Audit)(nil),
		(*authn.SignupVerification)(nil),
	} {
		opt, present := optional[tk]
		if !present {
			t.Errorf("the descriptor does not require %T", tk)
		}
		if opt {
			t.Errorf("%T is optional, want required", tk)
		}
	}
	if opt, present := optional[(*log.Logger)(nil)]; !present || !opt {
		t.Error("the logger is not taken as an optional requirement")
	}
}
