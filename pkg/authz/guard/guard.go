// Package guard is the authorization enforcement module: it implements the
// authentication capability's decision seam so that a module asking whether an
// actor may do a thing within this capability is answered by this module's
// policy.
//
// This package delivers the decider and holds the closed translation from an
// action to the (object, action) pair the engine matches on. The request-path
// middleware and the route declaration surface are the same module's other faces
// and land here beside the decider; this file's package builds the decider
// alone.
package guard

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose requests are authorized.
const ModuleName = "authz.guard"

// The module registers itself. Importing this package is what puts the decider
// in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers authn.Decider exclusively: the seam has one implementation, and a
// second provider of it fails the startup. It requires the engine and the
// catalog, both of which the policy module delivers exclusively, so a command
// that asks this module to decide names the policy module alongside it: an
// engine with no policy to match against is not a decider, and the catalog is
// what the route-declaration surface checks a declared permission against.
//
// The logger and the meter provider are optional: the decision reads neither,
// and their absence is a legal configuration.
//
// Prepare states StateEnabled and never StateAuto: the module taken in at run
// level must start, so whether it runs is a decision of the command tree.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authz.Enforcer)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*observability.MeterProvider)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*authn.Decider)(nil), Exclusive: true}},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			enforcer, err := core.Resolve[authz.Enforcer](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the enforcement engine: %w", ModuleName, err)
			}
			return &product{enforcer: enforcer}, nil
		},
	}
}

// product is this module's product and the implementation of authn.Decider.
type product struct {
	enforcer authz.Enforcer
}

var _ authn.Decider = (*product)(nil)
