// Package translation holds the permission-to-(object, action) mapping the
// authorization engine matches on: the rows this capability carries for its own
// permissions, and the union of them with every row a registered module declares
// through authz.TranslationSpec.
//
// It is a package of its own because two modules need it and neither owns the
// other: the policy module resolves it where the policy data is written, and the
// enforcement module reads it on the request path. What both of them, and the
// management surface besides, have to agree on is one mapping for the whole
// process, which is why the resolved set lives here rather than in either of
// them.
//
// It imports the root package and nothing else under this directory, so the
// leaves may import it and the root package may not — the direction the
// packaging decision asks for. The root package carries the interface through
// which a caller outside this directory reaches it: authz.Catalog.ObjectAction.
package translation

import (
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// pair is one row of the translation: the object and the action the engine
// matches on for a permission.
type pair struct{ object, action string }

// builtin is the set of rows this capability carries for its own permissions:
// the (object, action) pair the engine matches on for each. The keys and the
// permission constants are one vocabulary; the parity guard in package authz
// holds the set to every action the authentication side asks about.
//
// The set is the capability's own and is never rewritten. A module that declares
// a permission beyond it declares a row of its own alongside through
// authz.TranslationSpec; Resolve unions the two into the process's translation.
// The engine matches an object and an action verbatim, so a permission the
// process's translation has no row for is a decision the authorization layer
// cannot express: a request for one is refused rather than mapped by a rule of
// its own, and an action the authentication side added is refused until a row is
// added here.
var builtin = map[authz.Permission]pair{
	authz.PermissionTenantAccess:        {object: "tenant", action: "access"},
	authz.PermissionAccountRead:         {object: "account", action: "read"},
	authz.PermissionAccountCreate:       {object: "account", action: "create"},
	authz.PermissionAccountUpdate:       {object: "account", action: "update"},
	authz.PermissionAccountDisable:      {object: "account", action: "disable"},
	authz.PermissionAccountDelete:       {object: "account", action: "delete"},
	authz.PermissionMembershipManage:    {object: "membership", action: "manage"},
	authz.PermissionSessionRead:         {object: "session", action: "read"},
	authz.PermissionSessionRevoke:       {object: "session", action: "revoke"},
	authz.PermissionMFAReset:            {object: "factor", action: "reset"},
	authz.PermissionClientManage:        {object: "client", action: "manage"},
	authz.PermissionIDPManage:           {object: "idp", action: "manage"},
	authz.PermissionImpersonateStart:    {object: "impersonation", action: "start"},
	authz.PermissionImpersonationRead:   {object: "impersonation", action: "read"},
	authz.PermissionImpersonationRevoke: {object: "impersonation", action: "revoke"},
	authz.PermissionRoleRead:            {object: "role", action: "read"},
	authz.PermissionRoleManage:          {object: "role", action: "manage"},
	authz.PermissionAuditRead:           {object: "audit", action: "read"},
	authz.PermissionAuditExport:         {object: "audit", action: "export"},
	authz.PermissionSettingsRead:        {object: "settings", action: "read"},
	authz.PermissionSettingsWrite:       {object: "settings", action: "write"},
}

// process is the process's resolved translation set: the rows this capability
// carries unioned with the rows the registered modules declare through
// authz.TranslationSpec. The policy module and the enforcement module each
// resolve it in their Init, before any route declaration is checked and before
// any request is served; the two agree by construction, because resolving is a
// pure function of the registry. Until the first of them runs the set is nil,
// and a reader then sees the built-in rows alone — which is the whole set in a
// process that carries no module-declared row.
//
// It is a package value because the request middleware, the decider, the route
// declaration check and the management surface all read it, and none of them
// holds the product that resolved it. What makes one value safe is that a
// process resolves it once per drive, from one registry; a process that drives
// more than one assembly — a test binary over several registries — has the last
// resolution win, and each assembly hands it back on the way out (Reset, from
// the beats that end a drive) so that no drive reads another's set.
var process atomic.Pointer[map[authz.Permission]pair]

// Lookup returns the (object, action) pair a permission translates to, and
// whether the translation has a row for it at all.
//
// It is the one translation the request middleware, the decider, the route
// declaration check and the management surface all read, so a permission cannot
// mean one thing on one path and another on the other: they all read the
// process's resolved set, which Resolve built from the built-in rows and every
// module's declared rows. Before any assembly has resolved it, the built-in rows
// are the whole set.
func Lookup(permission authz.Permission) (object, action string, ok bool) {
	rows := builtin
	if resolved := process.Load(); resolved != nil {
		rows = *resolved
	}
	p, ok := rows[permission]
	return p.object, p.action, ok
}

// Builtin lists the permissions this capability carries a row for, in name
// order — the built-in set on its own, without the rows a module declared.
//
// It is for the reader that holds this capability's rows to something of its
// own: the parity guard in the parent directory is that reader, checking the
// built-in rows against the authentication side's actions and the permission
// constants. A caller asking what a permission translates to asks Lookup, which
// answers from the process's resolved set.
func Builtin() []authz.Permission {
	out := make([]authz.Permission, 0, len(builtin))
	for permission := range builtin {
		out = append(out, permission)
	}
	slices.Sort(out)
	return out
}

// Resolve builds the process's translation set — the built-in rows unioned with
// every row the registered modules declare — and installs it. The policy
// module's Init calls it once, before the declaration window closes.
//
// Three shapes of a declared row are refused, each wrapped around
// authz.ErrUnknownPermission, the same sentinel the declaration check uses for a
// permission the translation cannot express. A row with an empty object or
// action is refused: the engine matches a permission on its pair alone, so a
// permission whose pair is empty is one no grant can distinguishably carry. A
// row that names a permission already translated as another pair is refused: the
// built-in rows are this capability's own and a module has no business changing
// what one of them means, and two modules disagreeing on a row would leave a
// permission's meaning to iteration order. A row whose (object, action) pair
// already names a different permission is refused: the engine matches the pair
// verbatim and never sees the permission key, so two permissions sharing one
// pair are one permission to it, and a grant written for one would authorize the
// other wherever their domains overlap — the pair is therefore unique across the
// whole set. A row that agrees with one already there — the same permission, the
// same pair — is kept once, the way two modules declaring one permission share
// it.
//
// A failed resolution installs nothing: the set it would have built is dropped,
// and whatever a previous drive resolved stays as it was.
func Resolve(reg *core.Registry) error {
	rows := make(map[authz.Permission]pair, len(builtin))
	owners := make(map[pair]authz.Permission, len(builtin))
	for permission, row := range builtin {
		rows[permission] = row
		owners[row] = permission
	}
	for _, resource := range core.Resources[authz.TranslationSpec](reg) {
		for permission, declared := range resource.Value.Rows {
			row := pair{object: declared.Object, action: declared.Action}
			if row.object == "" || row.action == "" {
				return fmt.Errorf("%w: %s declares %q with an empty object or action (%q, %q)",
					authz.ErrUnknownPermission, resource.Module, permission, row.object, row.action)
			}
			if existing, ok := rows[permission]; ok {
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
			rows[permission] = row
			owners[row] = permission
		}
	}
	process.Store(&rows)
	return nil
}

// Reset drops the resolved set, leaving the built-in rows as the whole
// translation. An assembly that does not come up calls it, so that a later
// assembly sharing the process does not read a set this one never accepted.
func Reset() { process.Store(nil) }
