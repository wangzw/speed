package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/core"
)

const descriptionPrefix = "authz.permission."

const (
	groupAuthorization = "authorization"
	groupMembership    = "membership"
	groupIdentity      = "identity"
	groupSettings      = "settings"
	groupProfile       = "profile"
)

var permissions = declaredPermissions()

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

type catalog struct {
	declared    []authz.Permission
	set         map[authz.Permission]bool
	keys        map[authz.Permission]string
	metadata    map[authz.Permission]authz.PermissionMeta
	governed    []authz.Permission
	governedSet map[authz.Permission]bool
	// reverse maps an (object, action) pair to its permission; translation rows are unique.
	reverse map[[2]string]authz.Permission
}

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

func (c *catalog) PermissionOf(object, action string) (authz.Permission, bool) {
	p, ok := c.reverse[[2]string{object, action}]
	return p, ok
}

func (c *catalog) Declared() []authz.Permission {
	out := make([]authz.Permission, len(c.declared))
	copy(out, c.declared)
	return out
}

func (c *catalog) Has(p authz.Permission) bool { return c.set[p] }

func (c *catalog) TenantGoverned() []authz.Permission {
	out := make([]authz.Permission, len(c.governed))
	copy(out, c.governed)
	return out
}

func (c *catalog) Governed(p authz.Permission) bool { return c.governedSet[p] && c.set[p] }

func (c *catalog) ObjectAction(p authz.Permission) (object, action string, ok bool) {
	return translation.Lookup(p)
}

func (c *catalog) DescriptionKey(p authz.Permission) (string, bool) {
	if !c.set[p] {
		return "", false
	}
	return c.keys[p], true
}

func (c *catalog) Metadata(p authz.Permission) (authz.PermissionMeta, bool) {
	if !c.set[p] {
		return authz.PermissionMeta{}, false
	}
	return c.metadata[p], true
}
