package main

import (
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// authzBusinessModuleName is the name the host's authorization-vocabulary module
// is registered under. It carries the CRM's own business permissions and their
// translation, so that the authorization module's catalog holds them and its
// engine can match a route that names one. It owns no table and builds no
// product: it is a resource-only module, taken into a command for what its
// resources declare.
const authzBusinessModuleName = "authzbusiness"

// The host's own CRM business permissions.
//
// A permission names one thing a member may do in a tenant; the policy domain is
// the tenant, so a grant of one of these is a grant inside one tenant. They are
// the host's vocabulary and no permission constant of pkg/authz names them; the
// host declares each of them with the module below, so the catalog carries them
// and a route names them the way it names a carried permission. A carried
// permission (`settings.write`, `authn.impersonation.start`, …) is one the
// authorization capability already declares; only the host's own CRM actions are
// declared here.
const (
	permAccountRead  authz.Permission = "crm.account.read"
	permAccountWrite authz.Permission = "crm.account.write"
	permAccountDel   authz.Permission = "crm.account.delete"
	permAccountOwn   authz.Permission = "crm.account.own"
	permAccountPurge authz.Permission = "crm.account.purge"

	permContactRead   authz.Permission = "crm.contact.read"
	permContactWrite  authz.Permission = "crm.contact.write"
	permContactDel    authz.Permission = "crm.contact.delete"
	permContactExport authz.Permission = "crm.contact.export"

	permDealRead  authz.Permission = "crm.deal.read"
	permDealWrite authz.Permission = "crm.deal.write"
	permDealDel   authz.Permission = "crm.deal.delete"

	permPipelineRead  authz.Permission = "crm.pipeline.read"
	permPipelineWrite authz.Permission = "crm.pipeline.write"

	permActivityRead  authz.Permission = "crm.activity.read"
	permActivityWrite authz.Permission = "crm.activity.write"
	permActivityDel   authz.Permission = "crm.activity.delete"

	permFlowRead   authz.Permission = "crm.flow.read"
	permReportRead authz.Permission = "crm.report.read"

	permMemberRead   authz.Permission = "crm.member.read"
	permMemberManage authz.Permission = "crm.member.manage"
)

// businessPermissionKeys is the i18n key of each business permission's
// description, keyed by the permission. A description names what the permission
// lets a member do; the keys are the host's own, under a namespace of their own.
var businessPermissionKeys = map[authz.Permission]string{
	permAccountRead:  "crm.permission.account-read",
	permAccountWrite: "crm.permission.account-write",
	permAccountDel:   "crm.permission.account-delete",
	permAccountOwn:   "crm.permission.account-own",
	permAccountPurge: "crm.permission.account-purge",

	permContactRead:   "crm.permission.contact-read",
	permContactWrite:  "crm.permission.contact-write",
	permContactDel:    "crm.permission.contact-delete",
	permContactExport: "crm.permission.contact-export",

	permDealRead:  "crm.permission.deal-read",
	permDealWrite: "crm.permission.deal-write",
	permDealDel:   "crm.permission.deal-delete",

	permPipelineRead:  "crm.permission.pipeline-read",
	permPipelineWrite: "crm.permission.pipeline-write",

	permActivityRead:  "crm.permission.activity-read",
	permActivityWrite: "crm.permission.activity-write",
	permActivityDel:   "crm.permission.activity-delete",

	permFlowRead:   "crm.permission.flow-read",
	permReportRead: "crm.permission.report-read",

	permMemberRead:   "crm.permission.member-read",
	permMemberManage: "crm.permission.member-manage",
}

// businessPermissions lists every business permission, so its order is fixed and
// the spec and the tests do not depend on map iteration order.
var businessPermissions = []authz.Permission{
	permAccountRead, permAccountWrite, permAccountDel, permAccountOwn, permAccountPurge,
	permContactRead, permContactWrite, permContactDel, permContactExport,
	permDealRead, permDealWrite, permDealDel,
	permPipelineRead, permPipelineWrite,
	permActivityRead, permActivityWrite, permActivityDel,
	permFlowRead, permReportRead,
	permMemberRead, permMemberManage,
}

// businessTranslation is the (object, action) pair the policy engine matches on
// for each business permission.
//
// The objects are the bare resource nouns the host's own routes act on —
// `contact`, `deal`, `pipeline` — and the host's account is its own resource,
// the CRM account, under the object `crm-account`: the identity system carries
// `account` for `authn.account.*`, and the engine matches a (object, action)
// pair and never the permission key, so the two may not share a pair. The
// translation is unique across the whole set the process resolves — the host's
// rows and every carried row — so a grant written for one permission never
// authorizes another.
var businessTranslation = map[authz.Permission]authz.Translation{
	permAccountRead:  {Object: "crm-account", Action: "read"},
	permAccountWrite: {Object: "crm-account", Action: "write"},
	permAccountDel:   {Object: "crm-account", Action: "delete"},
	permAccountOwn:   {Object: "crm-account", Action: "own"},
	permAccountPurge: {Object: "crm-account", Action: "purge"},

	permContactRead:   {Object: "contact", Action: "read"},
	permContactWrite:  {Object: "contact", Action: "write"},
	permContactDel:    {Object: "contact", Action: "delete"},
	permContactExport: {Object: "contact", Action: "export"},

	permDealRead:  {Object: "deal", Action: "read"},
	permDealWrite: {Object: "deal", Action: "write"},
	permDealDel:   {Object: "deal", Action: "delete"},

	permPipelineRead:  {Object: "pipeline", Action: "read"},
	permPipelineWrite: {Object: "pipeline", Action: "write"},

	permActivityRead:  {Object: "activity", Action: "read"},
	permActivityWrite: {Object: "activity", Action: "write"},
	permActivityDel:   {Object: "activity", Action: "delete"},

	permFlowRead:   {Object: "flow", Action: "read"},
	permReportRead: {Object: "report", Action: "read"},

	permMemberRead:   {Object: "member", Action: "read"},
	permMemberManage: {Object: "member", Action: "manage"},
}

// businessPermissionSpec is the host's declaration of its own permissions: the
// list, and the i18n key of each one's description. The policy module's catalog
// is built by unioning the specs the registry holds, so declaring it here is what
// puts every business permission into the catalog a route declaration is checked
// against.
func businessPermissionSpec() authz.PermissionSpec {
	keys := make(map[authz.Permission]string, len(businessPermissions))
	for _, p := range businessPermissions {
		keys[p] = businessPermissionKeys[p]
	}
	return authz.PermissionSpec{Permissions: append([]authz.Permission(nil), businessPermissions...), DescriptionKeys: keys}
}

// businessTranslationSpec is the host's declaration of how each of its own
// permissions translates: the row the engine matches on for it. The enforcement
// module resolves it together with the rows the authorization capability carries,
// so a route the host declares on a business permission passes the startup's
// translation check and the middleware decides it on the pair named here.
func businessTranslationSpec() authz.TranslationSpec {
	rows := make(map[authz.Permission]authz.Translation, len(businessTranslation))
	for p, row := range businessTranslation {
		rows[p] = row
	}
	return authz.TranslationSpec{Rows: rows}
}

// businessObjectAction resolves a business permission to its (object, action)
// row and reports whether the permission is one the host declared. A carried
// permission is not in it: this package's map holds the host's own CRM actions
// alone.
func businessObjectAction(perm authz.Permission) (object, action string, ok bool) {
	row, found := businessTranslation[perm]
	if !found {
		return "", "", false
	}
	return row.Object, row.Action, true
}

// init registers the host's authorization-vocabulary module. It runs on serve:
// the enforcement module resolves the translation and builds the catalog there,
// and this module's resources are what carry the business permissions and their
// rows into both.
func init() {
	registerApplication(application{
		module: authzBusinessModule(),
		runsOn: []string{serveCommand},
	})
}

// authzBusinessModule declares the host's own permissions and their translation.
// It is resource-only: it constructs nothing and delivers no capability, so a
// command that runs no authorization still carries it without cost, and the two
// resources are all the enforcement module reads from it.
func authzBusinessModule() core.Module {
	return core.Module{
		Name: authzBusinessModuleName,
		Resources: []any{
			businessPermissionSpec(),
			businessTranslationSpec(),
		},
	}
}
