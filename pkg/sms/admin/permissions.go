package admin

import "github.com/vislake/speed/pkg/authz"

const (
	// PermissionTemplateRead is what a caller needs to read the template set.
	PermissionTemplateRead authz.Permission = "sms.template.read"
	// PermissionTemplateManage is what a caller needs to register a template,
	// offer one to the vendor for review, enable or disable one, or remove one.
	PermissionTemplateManage authz.Permission = "sms.template.manage"
)

const templateObject = "template"

func permissionSpec() authz.PermissionSpec {
	return authz.PermissionSpec{
		Permissions: []authz.Permission{PermissionTemplateRead, PermissionTemplateManage},
		DescriptionKeys: map[authz.Permission]string{
			PermissionTemplateRead:   "sms.permission.template-read",
			PermissionTemplateManage: "sms.permission.template-manage",
		},
	}
}

func translationSpec() authz.TranslationSpec {
	return authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
		PermissionTemplateRead:   {Object: templateObject, Action: "read"},
		PermissionTemplateManage: {Object: templateObject, Action: "manage"},
	}}
}
