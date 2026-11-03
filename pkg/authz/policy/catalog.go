package policy

import (
	"fmt"
	"slices"
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
		authz.PermissionTenantRoleRead,
		authz.PermissionTenantRoleManage,
		authz.PermissionAuditRead,
		authz.PermissionAuditExport,
		authz.PermissionSettingsRead,
		authz.PermissionSettingsWrite,
		authz.PermissionTenantSettingsRead,
		authz.PermissionTenantSettingsWrite,
	}
	keys := make(map[authz.Permission]string, len(list))
	replacer := strings.NewReplacer(".", "-", ":", "-")
	for _, p := range list {
		keys[p] = descriptionPrefix + replacer.Replace(string(p))
	}
	// The governed set: the permissions with a tenant-scoped endpoint that a
	// tenant administrator may hold and grant. It is closed, and the others this
	// capability declares stay platform-only. A permission added later joins it
	// only by being listed here.
	governed := []authz.Permission{
		authz.PermissionTenantRoleRead,
		authz.PermissionTenantRoleManage,
		authz.PermissionTenantSettingsRead,
		authz.PermissionTenantSettingsWrite,
		authz.PermissionMembershipManage,
		authz.PermissionIDPManage,
	}
	return authz.PermissionSpec{Permissions: list, TenantGoverned: governed, DescriptionKeys: keys}
}

// catalog is the permission catalog: the set of permissions the registered
// modules declare, built once in New and read-only afterwards.
type catalog struct {
	declared []authz.Permission
	set      map[authz.Permission]bool
	keys     map[authz.Permission]string
	// governed is the governed set in permission-name order, and governedSet is
	// its membership test. A governed permission is always declared: buildCatalog
	// refuses a module that marks one governed without listing it.
	governed    []authz.Permission
	governedSet map[authz.Permission]bool
	// reverse maps an (object, action) pair back to the permission it
	// translates to. The engine matches the pair and never sees the permission,
	// so a set of held pairs is only a set of permission names through this
	// map. The translation's rows are unique — Resolve refuses two permissions
	// sharing one pair — so the reverse is a function.
	reverse map[[2]string]authz.Permission
}

// buildCatalog collects every module's PermissionSpec and unions them.
//
// The catalog answers whether a permission is declared, which is what a route
// registration is held to: a route that names a permission no module declared
// is refused at startup rather than carrying a permission that matches no
// grant. The union may name the same permission twice across modules, which is
// one permission declared by two of them and is kept once.
//
// A module's TenantGoverned must be a subset of its own Permissions. One that
// marks a permission governed without declaring it is refused with
// ErrUnknownPermission: the governed set is the tenant administrator's whole
// grant set, and a permission nothing declares could never be granted or
// translated.
func buildCatalog(reg *core.Registry) (*catalog, error) {
	specs := core.Resources[authz.PermissionSpec](reg)
	c := &catalog{
		set:         make(map[authz.Permission]bool),
		keys:        make(map[authz.Permission]string),
		governedSet: make(map[authz.Permission]bool),
	}
	for _, resource := range specs {
		declaredHere := make(map[authz.Permission]bool, len(resource.Value.Permissions))
		for _, p := range resource.Value.Permissions {
			declaredHere[p] = true
			if !c.set[p] {
				c.set[p] = true
				c.declared = append(c.declared, p)
			}
			if key, ok := resource.Value.DescriptionKeys[p]; ok {
				c.keys[p] = key
			}
		}
		for _, p := range resource.Value.TenantGoverned {
			if !declaredHere[p] {
				return nil, fmt.Errorf("%w: %s marks %q governed without listing it in its Permissions",
					authz.ErrUnknownPermission, resource.Module, p)
			}
			if !c.governedSet[p] {
				c.governedSet[p] = true
				c.governed = append(c.governed, p)
			}
		}
	}
	slices.Sort(c.governed)
	c.indexReverse()
	return c, nil
}

// indexReverse rebuilds the pair-to-permission map from the process's
// translation. It runs when the catalog is built and again in the policy
// module's Init, after the translation is resolved: a module may declare a row
// of its own, and the map has to hold every row the process carries.
func (c *catalog) indexReverse() {
	reverse := make(map[[2]string]authz.Permission, len(c.declared))
	for _, p := range c.declared {
		object, action, ok := c.ObjectAction(p)
		if !ok {
			continue
		}
		reverse[[2]string{object, action}] = p
	}
	c.reverse = reverse
}

// permissionOf returns the permission a held (object, action) pair translates
// to, and whether any declared permission translates to it at all. A pair no
// declared permission carries is left out of a permission read: the policy
// table can hold a grant written against a vocabulary this process no longer
// translates, and such a grant names no permission a caller could act on.
func (c *catalog) permissionOf(object, action string) (authz.Permission, bool) {
	p, ok := c.reverse[[2]string{object, action}]
	return p, ok
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

// TenantGoverned returns the governed set in permission-name order. The slice
// is a copy: a caller sorting or trimming it does not disturb what the next
// caller sees.
func (c *catalog) TenantGoverned() []authz.Permission {
	out := make([]authz.Permission, len(c.governed))
	copy(out, c.governed)
	return out
}

// Governed reports whether p is a declared permission in the governed set.
func (c *catalog) Governed(p authz.Permission) bool { return c.governedSet[p] && c.set[p] }

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
