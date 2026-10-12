package main

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// deciderModuleName is the name the host's decision-seam module is registered
// under.
const deciderModuleName = "authz"

// opsDecider is the host's implementation of authn.Decider: the permission
// model's placeholder, and the user of the seam the identity system leaves for
// one. It answers three families of question, one per action, and no others.
//
// A platform management action is allowed to the one operator of the static
// credential table, judged by the account's full identifier (static.<operator
// subject>) and not by the static prefix alone, so a second subject of that
// table is refused like anyone else. This covers the account, membership,
// session, factor, secret, SSO-provider and impersonation-record routes the
// administration module mounts. It is the example the identity design gives of
// a host bootstrapping its first administrator from the credential table.
//
// Starting an impersonation is a management action the operator cannot take: a
// static subject has no session to hang the record on, so the identity design
// would refuse it whatever this seam answered. The host therefore answers it
// with a tenant rule instead, read from the tenant's own member_roles table: a
// tenant's owner may impersonate a member of the same tenant whose role ranks
// below the owner's. That is the placeholder for "who may impersonate whom",
// which the identity design leaves to this seam; it keeps the impersonation
// inside one tenant and never above the actor's own rank.
//
// A non-member entering a tenant (authn.tenant.access) is always refused, so an
// operator stays a stranger to every tenant: the admission layer asks this and,
// on false, answers the same 404 it answers a member the tenant does not hold.
// Until a permission model is written, no platform role crosses a tenant.
type opsDecider struct {
	released
	db db.Database
}

// opsDecider implements the seam it stands for.
var _ authn.Decider = (*opsDecider)(nil)

// Decide answers one question. Tenant access is refused whatever the actor; a
// start of an impersonation is judged by the actor's role in the target tenant;
// every other action is allowed to the operator account alone.
func (d *opsDecider) Decide(ctx context.Context, dec authn.Decision) (bool, error) {
	switch dec.Action {
	case authn.ActionTenantAccess:
		return false, nil
	case authn.ActionImpersonateStart:
		return d.mayImpersonate(ctx, dec)
	default:
		account := dec.Actor.Account()
		return account.IsStatic() && account == operatorAccount, nil
	}
}

// mayImpersonate answers whether the actor may start an impersonation of the
// Decision's target inside the Decision's tenant: the actor holds the owner role
// there and the target holds a lower one. A static actor, a missing tenant or a
// missing target is refused; a role that cannot be read is answered as an error,
// which the caller treats as an outage and never as permission.
func (d *opsDecider) mayImpersonate(ctx context.Context, dec authn.Decision) (bool, error) {
	actor := dec.Actor.Account()
	if actor.IsStatic() || dec.Tenant == "" || dec.TargetAccount == "" {
		return false, nil
	}
	// The role lives in the tenant's own table and the request carries no tenant
	// context of its own, so the read names the tenant and crosses tenant scope
	// with it, the way the account endpoint's GET /me does.
	scope := tenant.CrossTenant(ctx)
	id := tenant.ID(dec.Tenant)
	actorRole, err := roleIn(scope, d.db, id, actor)
	if err != nil {
		return false, err
	}
	if actorRole != roleOwner {
		return false, nil
	}
	targetRole, err := roleIn(scope, d.db, id, authn.AccountID(dec.TargetAccount))
	if err != nil {
		return false, err
	}
	return roleRank(targetRole) < roleRank(roleOwner), nil
}

// init registers the host's decision seam. It runs on serve: the admission layer
// takes it up there for authn.tenant.access, and the administration and
// impersonation modules ask it for their management and start decisions.
func init() {
	registerApplication(application{
		module: opsDeciderModule(),
		runsOn: []string{serveCommand},
	})
}

// opsDeciderModule delivers the host's Decider. The seam is exclusive, so a
// command names this module to provide one; it holds a logger for its release
// records and the database handle its impersonation rule reads member_roles
// through.
func opsDeciderModule() core.Module {
	return core.Module{
		Name: deciderModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
		},
		Provides: []core.Provision{{Token: (*authn.Decider)(nil), Exclusive: true}},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, deciderModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			return &opsDecider{released: released{log: logger}, db: database}, nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
