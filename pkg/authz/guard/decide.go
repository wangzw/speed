package guard

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// pair is one row of the translation: the object and the action the engine
// matches on for a permission.
type pair struct{ object, action string }

// translation is the closed set of rows from a permission to the (object,
// action) pair the engine matches on. The keys and the permission constants are
// one vocabulary; the parity guard holds the set to every action the
// authentication side asks about.
//
// The set is closed on purpose. The engine matches an object and an action
// verbatim, so a permission with no row is a decision this module cannot
// express; a request for one is refused rather than mapped by a rule of its
// own, and an action the authentication side added is refused until this table
// is updated.
var translation = map[string]pair{
	string(authz.PermissionTenantAccess):        {object: "tenant", action: "access"},
	string(authz.PermissionAccountRead):         {object: "account", action: "read"},
	string(authz.PermissionAccountCreate):       {object: "account", action: "create"},
	string(authz.PermissionAccountUpdate):       {object: "account", action: "update"},
	string(authz.PermissionAccountDisable):      {object: "account", action: "disable"},
	string(authz.PermissionAccountDelete):       {object: "account", action: "delete"},
	string(authz.PermissionMembershipManage):    {object: "membership", action: "manage"},
	string(authz.PermissionSessionRead):         {object: "session", action: "read"},
	string(authz.PermissionSessionRevoke):       {object: "session", action: "revoke"},
	string(authz.PermissionMFAReset):            {object: "factor", action: "reset"},
	string(authz.PermissionClientManage):        {object: "client", action: "manage"},
	string(authz.PermissionIDPManage):           {object: "idp", action: "manage"},
	string(authz.PermissionImpersonateStart):    {object: "impersonation", action: "start"},
	string(authz.PermissionImpersonationRead):   {object: "impersonation", action: "read"},
	string(authz.PermissionImpersonationRevoke): {object: "impersonation", action: "revoke"},
	string(authz.PermissionRoleRead):            {object: "role", action: "read"},
	string(authz.PermissionRoleManage):          {object: "role", action: "manage"},
	string(authz.PermissionAuditRead):           {object: "audit", action: "read"},
	string(authz.PermissionAuditExport):         {object: "audit", action: "export"},
	string(authz.PermissionSettingsRead):        {object: "settings", action: "read"},
	string(authz.PermissionSettingsWrite):       {object: "settings", action: "write"},
}

// ObjectAction returns the (object, action) pair a permission translates to,
// and whether the permission has a row at all.
//
// It is the one translation the decider and, when it lands here, the request
// middleware both read, so a permission cannot mean one thing on one path and
// another on the other. The parity guard reads it to hold the set to every
// action the authentication side asks about.
func ObjectAction(permission authz.Permission) (object, action string, ok bool) {
	p, ok := translation[string(permission)]
	return p.object, p.action, ok
}

// Decide implements authn.Decider.
//
// It translates the action to a query and puts it to the engine. The subject is
// the actor's accountability key and the domain is the tenant the decision
// names, or the platform domain when it names none. An action with no row in
// the translation is refused without an error: the authentication side may add
// an action this table does not know, and the safe answer is a denial rather
// than an allow or a failure the caller reads as an outage.
//
// authn.tenant.access is the one action with a platform-domain fallback: a
// cross-tenant operator holds their role in the platform domain, so a query
// that does not match in the tenant's own domain is put again in the platform
// domain before it is denied. Every other action decides in one domain.
//
// An engine error is returned as it stands and is neither a denial nor a
// permission: the caller reads it as an outage.
func (p *product) Decide(ctx context.Context, d authn.Decision) (bool, error) {
	object, action, ok := ObjectAction(authz.Permission(d.Action))
	if !ok {
		return false, nil
	}
	subject := d.Actor.Key()
	domain := authz.Domain(d.Tenant)
	if domain == "" {
		domain = authz.PlatformDomain
	}

	allowed, err := p.allowed(ctx, subject, domain, object, action)
	if err != nil {
		return false, err
	}
	if allowed {
		return true, nil
	}
	if d.Action == authn.ActionTenantAccess && domain != authz.PlatformDomain {
		return p.allowed(ctx, subject, authz.PlatformDomain, object, action)
	}
	return false, nil
}

// allowed puts one query to the engine for a subject in a domain.
func (p *product) allowed(ctx context.Context, subject string, domain authz.Domain, object, action string) (bool, error) {
	return p.enforcer.Allowed(ctx, authz.Query{
		Subject: subject,
		Domain:  domain,
		Object:  object,
		Action:  action,
	})
}
