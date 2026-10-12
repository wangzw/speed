package guard

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// pair is one row of the translation: the object and the action the engine
// matches on for a permission.
type pair struct{ object, action string }

// translation is the set of rows this capability carries for its own
// permissions: the (object, action) pair the engine matches on for each. The
// keys and the permission constants are one vocabulary; the parity guard holds
// the set to every action the authentication side asks about.
//
// The set is the capability's own and is never rewritten. A module that declares
// a permission beyond it declares a row of its own alongside through
// authz.TranslationSpec; resolveTranslation unions the two into the process's
// translation. The engine matches an object and an action verbatim, so a
// permission the process's translation has no row for is a decision this module
// cannot express: a request for one is refused rather than mapped by a rule of
// its own, and an action the authentication side added is refused until a row is
// added here.
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

// processTranslation is the process's resolved translation set: the rows this
// capability carries unioned with the rows the registered modules declare
// through authz.TranslationSpec. The module's Init resolves it once, before any
// route declaration is checked and before any request is served. It is nil until
// then, and it stays nil in a process that carries no module-declared row, so
// the built-in rows are the whole set and a reader outside a driven assembly
// reads them alone.
//
// It is a package value because the request middleware, the decider and the
// management surface all read it through ObjectAction, and none of them holds
// the guard product. The single-assembly invariant that makes one value safe is
// that a process wires one guard module, whose Init resolves the set once. A
// process that drives more than one assembly — a test binary over several
// registries, or a command tree that assembles more than one guard — has the
// last resolution win, so each assembly clears it between drives; Start clears
// it when it fails, so a failed assembly does not leave its set serving a route
// it never accepted.
var processTranslation atomic.Pointer[map[string]pair]

// ObjectAction returns the (object, action) pair a permission translates to,
// and whether the translation has a row for it at all.
//
// It is the one translation the request middleware, the decider and the
// management surface all read, so a permission cannot mean one thing on one path
// and another on the other: they all read the process's resolved set, which
// Init built from the built-in rows and every module's declared rows. The parity
// guard reads it to hold the built-in rows to every action the authentication
// side asks about.
func ObjectAction(permission authz.Permission) (object, action string, ok bool) {
	rows := translation
	if resolved := processTranslation.Load(); resolved != nil {
		rows = *resolved
	}
	p, ok := rows[string(permission)]
	return p.object, p.action, ok
}

// resolveTranslation builds the process's translation set — the built-in rows
// unioned with every row the registered modules declare — and installs it. It is
// called once, from Init.
//
// Three shapes of a declared row are refused at startup, each wrapped around
// authz.ErrUnknownPermission, the same sentinel the declaration check uses for a
// permission the translation cannot express. A row with an empty object or
// action is refused: the engine matches a permission on its pair alone, so a
// permission whose pair is empty is one no grant can distinguishably carry. A
// row that names a permission already translated as another pair is refused: the
// built-in rows are this capability's own and a module has no business changing
// what one of them means, and two modules disagreeing on a row would leave a
// permission's meaning to iteration order. A row whose (object, action) pair
// already names a different permission is refused: the engine matches the pair
// verbatim and never sees the permission key, so two permissions sharing one pair
// are one permission to it, and a grant written for one would authorize the other
// wherever their domains overlap — the pair is therefore unique across the whole
// set. A row that agrees with one already there — the same permission, the same
// pair — is kept once, the way two modules declaring one permission share it.
func resolveTranslation(reg *core.Registry) error {
	rows := make(map[string]pair, len(translation))
	owners := make(map[pair]string, len(translation))
	for key, row := range translation {
		rows[key] = row
		owners[row] = key
	}
	for _, resource := range core.Resources[authz.TranslationSpec](reg) {
		for permission, declared := range resource.Value.Rows {
			row := pair{object: declared.Object, action: declared.Action}
			if row.object == "" || row.action == "" {
				return fmt.Errorf("%w: %s declares %q with an empty object or action (%q, %q)",
					authz.ErrUnknownPermission, resource.Module, permission, row.object, row.action)
			}
			key := string(permission)
			if existing, ok := rows[key]; ok {
				if existing != row {
					return fmt.Errorf("%w: %s translates %q as (%q, %q), but it is already (%q, %q)",
						authz.ErrUnknownPermission, resource.Module, permission,
						row.object, row.action, existing.object, existing.action)
				}
				continue
			}
			if other, ok := owners[row]; ok {
				return fmt.Errorf("%w: %s translates %q as (%q, %q), a pair already carried by %q: "+
					"the engine matches the pair and not the permission, so the two would be one",
					authz.ErrUnknownPermission, resource.Module, permission, row.object, row.action, other)
			}
			rows[key] = row
			owners[row] = key
		}
	}
	processTranslation.Store(&rows)
	return nil
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
