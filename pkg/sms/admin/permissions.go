package admin

import "github.com/vislake/speed/pkg/authz"

// The permissions of this management surface. The sending capability names no
// permission of its own — a send is reached through a consumer's own route — so
// the vocabulary a route of this surface is held to is this module's, and this
// module declares it: the two permissions reach the process's permission
// catalog through authz.PermissionSpec and the pair each one decides on reaches
// the process's translation through authz.TranslationSpec.
//
// The translation is what makes the decision answerable at all. The policy
// engine matches a permission on its (object, action) pair and never on the
// permission key, and a permission the process's translation has no row for is a
// decision the guard cannot express — it answers a denial, so a route held to
// either of these would refuse every caller. Without the spec beside it the
// permissions would not be declared, and a host's permission picker would not
// list them.
const (
	// PermissionTemplateRead is what a caller needs to read the template set.
	PermissionTemplateRead authz.Permission = "sms.template.read"
	// PermissionTemplateManage is what a caller needs to register a template,
	// offer one to the vendor for review, enable or disable one, or remove one.
	PermissionTemplateManage authz.Permission = "sms.template.manage"
)

// templateObject is the resource noun both permissions act on. The engine
// matches the pair and never the permission key, so the pair is unique across
// the whole process set: this object carries no other capability's action.
const templateObject = "template"

// permissionSpec is this module's declaration of the two permissions, with the
// i18n key of each one's description. The policy module's catalog is built by
// unioning the specs the registry holds, so declaring it here is what puts both
// permissions into the catalog a route declaration is checked against and a
// picker is read from.
func permissionSpec() authz.PermissionSpec {
	return authz.PermissionSpec{
		Permissions: []authz.Permission{PermissionTemplateRead, PermissionTemplateManage},
		DescriptionKeys: map[authz.Permission]string{
			PermissionTemplateRead:   "sms.permission.template-read",
			PermissionTemplateManage: "sms.permission.template-manage",
		},
	}
}

// translationSpec is this module's declaration of how each permission
// translates: the row the engine matches on for it. The enforcement module
// resolves it together with the rows the authorization capability carries, so a
// decision on either permission is expressed, and a route held to one decides
// on the pair named here.
func translationSpec() authz.TranslationSpec {
	return authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
		PermissionTemplateRead:   {Object: templateObject, Action: "read"},
		PermissionTemplateManage: {Object: templateObject, Action: "manage"},
	}}
}
