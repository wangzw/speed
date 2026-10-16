package policy

import (
	"strings"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/core"
)

// descriptionPrefix is the namespace every permission's description key is
// built under. The keys are this module's own i18n paths; nothing about a grant
// or a binding names them.
const descriptionPrefix = "authz.permission."

// permissions is the spec this module declares: every permission it carries,
// which is every permission of the capability directory — the actions asked
// about over the decision seam, this capability's own management surface, and
// the audit and settings surfaces it carries.
//
// The list is declared as a resource so that the catalog is built from data the
// registry holds rather than from a second list in code, and so that a module
// outside this directory declaring a permission of its own adds it the same
// way. The word-table guard holds this list to the root package's constants: a
// constant with no entry here, or an entry naming no constant, fails it.
var permissions = declaredPermissions()

// declaredPermissions builds the spec: the permission list, and a description
// key for each. The key is the namespace joined to the permission with its
// separators turned into dashes, so an added permission gets a key without a
// second list to edit.
func declaredPermissions() authz.PermissionSpec {
	list := []authz.Permission{
		authz.PermissionAccountRead,
		authz.PermissionAccountCreate,
		authz.PermissionAccountUpdate,
		authz.PermissionAccountDisable,
		authz.PermissionAccountDelete,
		authz.PermissionMembershipManage,
		authz.PermissionSessionRead,
		authz.PermissionSessionRevoke,
		authz.PermissionMFAReset,
		authz.PermissionClientManage,
		authz.PermissionIDPManage,
		authz.PermissionImpersonateStart,
		authz.PermissionImpersonationRead,
		authz.PermissionImpersonationRevoke,
		authz.PermissionTenantAccess,
		authz.PermissionRoleRead,
		authz.PermissionRoleManage,
		authz.PermissionAuditRead,
		authz.PermissionAuditExport,
		authz.PermissionSettingsRead,
		authz.PermissionSettingsWrite,
	}
	keys := make(map[authz.Permission]string, len(list))
	replacer := strings.NewReplacer(".", "-", ":", "-")
	for _, p := range list {
		keys[p] = descriptionPrefix + replacer.Replace(string(p))
	}
	return authz.PermissionSpec{Permissions: list, DescriptionKeys: keys}
}

// catalog is the permission catalog: the set of permissions the registered
// modules declare, built once in New and read-only afterwards.
type catalog struct {
	declared []authz.Permission
	set      map[authz.Permission]bool
	keys     map[authz.Permission]string
}

// buildCatalog collects every module's PermissionSpec and unions them.
//
// The catalog answers whether a permission is declared, which is what a route
// registration is held to: a route that names a permission no module declared
// is refused at startup rather than carrying a permission that matches no
// grant. The union may name the same permission twice across modules, which is
// one permission declared by two of them and is kept once.
func buildCatalog(reg *core.Registry) *catalog {
	specs := core.Resources[authz.PermissionSpec](reg)
	c := &catalog{
		set:  make(map[authz.Permission]bool),
		keys: make(map[authz.Permission]string),
	}
	for _, resource := range specs {
		for _, p := range resource.Value.Permissions {
			if !c.set[p] {
				c.set[p] = true
				c.declared = append(c.declared, p)
			}
			if key, ok := resource.Value.DescriptionKeys[p]; ok {
				c.keys[p] = key
			}
		}
	}
	return c
}

// Declared returns every declared permission. The slice is a copy: a caller
// sorting or trimming it does not disturb what the next caller sees.
func (c *catalog) Declared() []authz.Permission {
	out := make([]authz.Permission, len(c.declared))
	copy(out, c.declared)
	return out
}

// Has reports whether p is declared.
func (c *catalog) Has(p authz.Permission) bool { return c.set[p] }

// ObjectAction returns the (object, action) pair a permission translates to.
//
// The translation is the process's, resolved once by this module's Init from the
// built-in rows and every module's declared ones; the catalog does not hold a
// copy of its own, because a permission's meaning is one thing for the whole
// process and a second copy could only disagree with the first.
func (c *catalog) ObjectAction(p authz.Permission) (object, action string, ok bool) {
	return translation.Lookup(p)
}

// DescriptionKey returns the i18n key of p's description, and whether p is
// declared at all.
func (c *catalog) DescriptionKey(p authz.Permission) (string, bool) {
	if !c.set[p] {
		return "", false
	}
	return c.keys[p], true
}
