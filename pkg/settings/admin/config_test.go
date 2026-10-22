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

// The endpoint list refuses a shape the module cannot run on, naming the key: an
// empty list mounts the routes nowhere, the name that stands for every endpoint
// would put them on the public endpoints, an empty name names no endpoint, and a
// name written twice would register every route on it twice.
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

// The schema declares the endpoint list as a required item taking the primary
// source alone: it decides where the management routes are mounted, so it must
// be named, and an environment variable or argument must not be able to move
// them.
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

// The descriptor requires the settings surface, the guard, the audit capability
// and the registration-verification read, and takes the logger optionally.
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
