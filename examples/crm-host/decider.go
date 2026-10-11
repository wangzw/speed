package main

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// deciderModuleName is the name the host's decision-seam module is registered
// under.
const deciderModuleName = "authz"

// opsDecider is the host's implementation of authn.Decider: the permission
// model's placeholder, and the first user of the seam the identity system leaves
// for one. It answers the two questions the modules on this command ask, and no
// others.
//
// A management action is allowed to the one operator of the static credential
// table, judged by the account's full identifier (static.<operator subject>) and
// not by the static prefix alone, so a second subject of that table is refused
// like anyone else. This is the example the identity design gives of a host
// bootstrapping its first administrator from the credential table.
//
// A non-member entering a tenant (authn.tenant.access) is always refused, so an
// operator stays a stranger to every tenant: the admission layer asks this and,
// on false, answers the same 404 it answers a member the tenant does not hold.
// Until a permission model is written, no platform role crosses a tenant.
type opsDecider struct {
	released
}

// opsDecider implements the seam it stands for.
var _ authn.Decider = (*opsDecider)(nil)

// Decide answers one question. Tenant access is refused whatever the actor; every
// other action is allowed to the operator account alone.
func (opsDecider) Decide(_ context.Context, d authn.Decision) (bool, error) {
	if d.Action == authn.ActionTenantAccess {
		return false, nil
	}
	account := d.Actor.Account()
	return account.IsStatic() && account == operatorAccount, nil
}

// init registers the host's decision seam. It runs on serve: the admission layer
// takes it up there for authn.tenant.access, and the administration and
// impersonation routes will ask it for the rest.
func init() {
	registerApplication(application{
		module: opsDeciderModule(),
		runsOn: []string{serveCommand},
	})
}

// opsDeciderModule delivers the host's Decider. The seam is exclusive, so a
// command names this module to provide one; it holds a logger for its release
// records and reads no configuration.
func opsDeciderModule() core.Module {
	return core.Module{
		Name:     deciderModuleName,
		Requires: []core.Requirement{{Token: (*log.Logger)(nil)}},
		Provides: []core.Provision{{Token: (*authn.Decider)(nil), Exclusive: true}},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, deciderModuleName)
			if err != nil {
				return nil, err
			}
			return &opsDecider{released: released{log: logger}}, nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
