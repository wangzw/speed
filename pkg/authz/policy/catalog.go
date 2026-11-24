package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/core"
)

// descriptionPrefix is the namespace every permission's description key is built
// under.
const descriptionPrefix = "authz.permission."

// The permission groups a management surface groups the catalog by.
const (
	groupAuthorization = "authorization"
	groupMembership    = "membership"
	groupIdentity      = "identity"
	groupSettings      = "settings"
	groupProfile       = "profile"
)

// permissions is the spec this module declares: every permission of the
// capability directory. The word-table guard holds this list to the root
// package's constants.
var permissions = declaredPermissions()

// declaredPermissions builds the spec: the permission list, a description key
// for each, and the governed set with its metadata.
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
		authz.PermissionTenantProfileRead,
		authz.PermissionTenantProfileWrite,
	}
	keys := make(map[authz.Permission]string, len(list))
	replacer := strings.NewReplacer(".", "-", ":", "-")
	for _, p := range list {
		keys[p] = descriptionPrefix + replacer.Replace(string(p))
	}
	// The governed set: the permissions with a tenant-scoped endpoint that a
	// tenant administrator may hold and grant.
	governed := []authz.Permission{
		authz.PermissionTenantRoleRead,
		authz.PermissionTenantRoleManage,
		authz.PermissionTenantSettingsRead,
		authz.PermissionTenantSettingsWrite,
		authz.PermissionTenantProfileRead,
		authz.PermissionTenantProfileWrite,
		authz.PermissionMembershipManage,
		authz.PermissionIDPManage,
	}
	// The governance metadata of the governed set.
	metadata := map[authz.Permission]authz.PermissionMeta{
		authz.PermissionTenantRoleRead:      {Group: groupAuthorization},
		authz.PermissionTenantRoleManage:    {Group: groupAuthorization, Dangerous: true},
		authz.PermissionMembershipManage:    {Group: groupMembership, Dangerous: true},
		authz.PermissionIDPManage:           {Group: groupIdentity, Dangerous: true},
		authz.PermissionTenantSettingsRead:  {Group: groupSettings},
		authz.PermissionTenantSettingsWrite: {Group: groupSettings, Dangerous: true},
		authz.PermissionTenantProfileRead:   {Group: groupProfile},
		authz.PermissionTenantProfileWrite:  {Group: groupProfile},
	}
	return authz.PermissionSpec{
		Permissions:     list,
		TenantGoverned:  governed,
		DescriptionKeys: keys,
		Metadata:        metadata,
	}
}

// catalog is the permission catalog: the set of permissions the registered
// modules declare, built once in New and read-only afterwards.
type catalog struct {
	declared    []authz.Permission
	set         map[authz.Permission]bool
	keys        map[authz.Permission]string
	metadata    map[authz.Permission]authz.PermissionMeta
	governed    []authz.Permission
	governedSet map[authz.Permission]bool
	// reverse maps an (object, action) pair back to its permission. The
	// translation's rows are unique, so the reverse is a function.
	reverse map[[2]string]authz.Permission
}

// buildCatalog collects every module's PermissionSpec and unions them. A module
// that marks a permission governed without declaring it is refused with
// ErrUnknownPermission.
func buildCatalog(reg *core.Registry) (*catalog, error) {
	specs := core.Resources[authz.PermissionSpec](reg)
	c := &catalog{
		set:         make(map[authz.Permission]bool),
		keys:        make(map[authz.Permission]string),
		metadata:    make(map[authz.Permission]authz.PermissionMeta),
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
			if meta, ok := resource.Value.Metadata[p]; ok {
				c.metadata[p] = meta
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
// translation.
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

// PermissionOf returns the permission a held (object, action) pair translates
// to, and whether any declared permission translates to it at all.
func (c *catalog) PermissionOf(object, action string) (authz.Permission, bool) {
	p, ok := c.reverse[[2]string{object, action}]
	return p, ok
}

// Declared returns every declared permission, as a copy.
func (c *catalog) Declared() []authz.Permission {
	out := make([]authz.Permission, len(c.declared))
	copy(out, c.declared)
	return out
}

// Has reports whether p is declared.
func (c *catalog) Has(p authz.Permission) bool { return c.set[p] }

// TenantGoverned returns the governed set in permission-name order, as a copy.
func (c *catalog) TenantGoverned() []authz.Permission {
	out := make([]authz.Permission, len(c.governed))
	copy(out, c.governed)
	return out
}

// Governed reports whether p is a declared permission in the governed set.
func (c *catalog) Governed(p authz.Permission) bool { return c.governedSet[p] && c.set[p] }

// ObjectAction returns the (object, action) pair a permission translates to,
// read from the process's translation.
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

// Metadata returns the governance metadata of p, and whether p is declared at
// all. A declared permission whose module supplied none answers the zero value.
func (c *catalog) Metadata(p authz.Permission) (authz.PermissionMeta, bool) {
	if !c.set[p] {
		return authz.PermissionMeta{}, false
	}
	return c.metadata[p], true
}
