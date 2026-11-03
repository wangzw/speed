package tenantadmin

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title is the module's name and the version
// is the version of the fragment's shape.
const (
	fragmentTitle   = "settings.tenantadmin"
	fragmentVersion = "1.0.0"
)

// The management routes, relative to the API root the module's Spec declares.
// The tenant scope puts the tenant in the address, so every path names it first;
// the routes are mounted on their own base path and not under a configurable
// one, and every one of them is addressed by a natural key — a setting key's
// namespace and the rest of the key — so that no opaque row identifier ever
// appears in a path.
const (
	pathSettings     = "/:tenant/settings"
	pathNamespace    = pathSettings + "/:namespace"
	pathNamespaceKey = pathSettings + "/:namespace/:key"
)

// The closed set of values the view's source takes: the layer the value a view
// reports comes from. A key with an explicit row on the tenant layer is tenant;
// otherwise a key with an explicit row on the global layer is global; otherwise
// the declared default is the value and the source is default.
const (
	sourceTenant  = "tenant"
	sourceGlobal  = "global"
	sourceDefault = "default"
)

// SettingsTenantAdminSetting is one declared setting item as the tenant
// management routes show it. It is built field by field from a declaration and
// the resolved value and never handed through as it stands, so that a field
// added to a declaration later does not reach a client by being there.
//
// Every value-bearing field is text: default is the declared default in the
// declared type's own text form, written is the text the tenant layer holds,
// and value is the text the item resolves to after the three-level fallback —
// tenant row, global row, declared default. They share one form, so a client
// compares and displays them without converting between a typed value and a row,
// and a duration reads "5m0s" rather than as a count of nanoseconds. A sensitive
// item's default is empty, and its written and value carry the mask string
// instead of the value.
//
// written alone cannot say whether the tenant layer holds a row: an empty text is
// both a missing row and a row written as the empty string. has_row carries that
// fact on its own, so a client can mark the item as the declared default or as an
// override without interpreting the text, and a sensitive item tells a client
// whether there is a row to remove even though its text is masked.
//
// source is the backend's answer to which layer the reported value comes from.
// It is computed here, from the tenant layer's rows and the global layer's rows,
// and never inferred by a client: this surface does not expose the global layer
// and a client cannot read it to work the source out.
type SettingsTenantAdminSetting struct {
	_ struct{} `description:"One declared setting item as the tenant management surface shows it. The declaration fields come from the key table; written and has_row come from that tenant layer's rows, and value and source from the resolved read. Every value-bearing field is text in the declared type's own form, so a duration reads as \"5m0s\" rather than as a count of nanoseconds."`

	Key         string `json:"key" required:"true" description:"The full setting key: the declaring module's namespace joined with the item's own segment by a dot."`
	Namespace   string `json:"namespace" required:"true" description:"The module that declared the item"`
	Type        string `json:"type" required:"true" description:"The declared type's name as reflection reports it, such as \"string\", \"int\", \"bool\" or \"time.Duration\"; the item's values are stored and rendered in that type's text form."`
	Description string `json:"description" required:"true" description:"The human-readable text the declaring module gave the item."`
	Domain      string `json:"domain" required:"true" description:"The declared rule over the item's values, empty when every value of the type is admitted"`
	Sensitive   bool   `json:"sensitive" required:"true" description:"Whether the item is a credential. A sensitive item's default is empty and its written and value carry a fixed mask string; has_row still reports whether the tenant layer holds a row."`
	Default     string `json:"default" required:"true" description:"The declared default in the declared type's text form, empty for a sensitive item"`
	Written     string `json:"written" required:"true" description:"The text the tenant layer holds, the mask string for a sensitive item"`
	HasRow      bool   `json:"has_row" required:"true" description:"Whether the tenant layer holds a row for the item. For a non-sensitive item, written is empty both when no row is held and when the row's text is empty; a sensitive item's written is the mask string whether or not a row exists."`
	Value       string `json:"value" required:"true" description:"The text the item resolves to after the three-level fallback, the mask string for a sensitive item"`
	Source      string `json:"source" required:"true" enum:"tenant,global,default" description:"The layer the reported value comes from: \"tenant\" when this tenant layer holds an explicit row, \"global\" when it does not and the global layer does, \"default\" when neither does and the value is the declared default."`
}

// listInput is the input of the all-items read: the tenant the path names.
type listInput struct {
	Tenant string `path:"tenant" required:"true" description:"The tenant whose settings layer the read addresses, named in the path. The layer and the policy domain are both fixed to it; a \"layer\" or \"tenant\" query parameter is refused with 400."`
}

// namespaceInput is the input of the per-namespace read: the tenant and the
// namespace path segment.
type namespaceInput struct {
	Tenant    string `path:"tenant" required:"true" description:"The tenant whose settings layer the read addresses, named in the path. A \"layer\" or \"tenant\" query parameter is refused with 400."`
	Namespace string `path:"namespace" description:"The namespace whose declared items the listing returns: the declaring module, which is the key's leading segment (for example \"deals\" or \"authn.session\"). A namespace no declared item belongs to, or one whose items are all platform-only, is a 404."`
}

// setInput is the input of the write route: the tenant, the two path segments of
// the full key, and the JSON body carrying the value. The body is bound by the
// entry point, which refuses a member the declaration does not carry.
type setInput struct {
	Tenant    string `path:"tenant" required:"true" description:"The tenant whose settings layer the write lands on, named in the path. A \"layer\" or \"tenant\" query parameter is refused with 400."`
	Namespace string `path:"namespace" description:"The namespace of the item to write: the declaring module, which is the key's leading segment. Joined with \"key\" by a dot to form the full key, which must be declared and not platform-only."`
	Key       string `path:"key" description:"The rest of the setting key under the namespace; dots are allowed. Joined with the namespace by a dot to form the full key, which must be declared and not platform-only."`
	Value     any    `json:"value" required:"true" description:"The value to store, in the JSON form the item's declared type is read from: a string for a string, duration, or text-decoding item, a number for a numeric item, a boolean for a bool item. It is encoded under the declared type and then held to the item's declared domain; a value that cannot be encoded, lies outside the domain, or exceeds the size limit answers 422."`
}

// removeInput is the input of the delete route: the tenant and the two path
// segments of the full key.
type removeInput struct {
	Tenant    string `path:"tenant" required:"true" description:"The tenant whose settings layer the row is dropped from, named in the path. A \"layer\" or \"tenant\" query parameter is refused with 400."`
	Namespace string `path:"namespace" description:"The namespace of the item whose row is dropped: the declaring module, which is the key's leading segment. Joined with \"key\" by a dot to form the full key, which must be declared and not platform-only."`
	Key       string `path:"key" description:"The rest of the setting key under the namespace; dots are allowed. Joined with the namespace by a dot to form the full key, which must be declared and not platform-only."`
}

// settingListType is the success body's type: the declared items, each as the
// tenant management surface shows it.
var settingListType = reflect.TypeOf([]SettingsTenantAdminSetting{})

// listOperation declares GET /{tenant}/settings: every declared item with the
// value it resolves to on the path tenant's layer. The handler returns one
// success body, so this is the simple form.
var listOperation = speedhttp.NewOperation[listInput, []SettingsTenantAdminSetting](
	"settings.tenantadmin.settings.list",
	http.MethodGet,
	pathSettings,
	"List every declared setting with its value on the tenant layer",
	"Returns every item in the declaration listing, including keys declared by modules that do not run "+
		"in this process, each with the value it resolves to on the layer of the tenant in the path. The "+
		"platform-only namespaces are left out. Resolution walks the three-level fallback: the tenant row, "+
		"then the global row, then the declared default; source reports which of the three the value came "+
		"from. Every value-bearing field is text in the declared type's own form; for a sensitive item "+
		"default is empty and written and value carry a fixed mask string. A failure to read the layer's "+
		"rows or to resolve a value answers 503 rather than leaving the item out.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every declared item",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A layer or tenant query parameter was sent, or the tenant in the path is outside the tenant identifier's value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.tenant.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// namespaceOperation declares GET /{tenant}/settings/:namespace: the declared
// items one namespace declares. The handler builds its own answer because a
// namespace no declared item belongs to is a bare 404, which the simple form
// cannot write.
var namespaceOperation = speedhttp.NewReplyOperation[namespaceInput](
	"settings.tenantadmin.settings.listNamespace",
	http.MethodGet,
	pathNamespace,
	"List the declared settings one namespace declares on the tenant layer",
	"Returns the declared items whose namespace equals the path segment, that is the group one module "+
		"declared, each carrying the same fields and resolved the same way as on the all-items route. The "+
		"declaration listing is not narrowed to the modules running in this process, and the platform-only "+
		"namespaces are left out. A namespace no declared item belongs to, or one whose items are all "+
		"platform-only, answers 404 without a problem document.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The namespace's declared items",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: settingListType},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A layer or tenant query parameter was sent, or the tenant in the path is outside the tenant identifier's value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.tenant.read in the tenant's domain"),
		{Status: http.StatusNotFound, Description: "No declared item belongs to the namespace, or the namespace is platform-only"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// setOperation declares PUT /{tenant}/settings/:namespace/:key: it writes one
// row on the path tenant's layer. The handler builds its own answer because a
// success is 204 with no body, an undeclared or platform-only key is a bare 404,
// and a write the host's seam refuses is a 403 protected.
var setOperation = speedhttp.NewReplyOperation[setInput](
	"settings.tenantadmin.settings.set",
	http.MethodPut,
	pathNamespaceKey,
	"Write one setting row on the tenant layer",
	"Writes one row for the setting named by the namespace and key path segments on the layer of the "+
		"tenant in the path. The full key `<namespace>.<key>` must match a declared item and must not belong "+
		"to a platform-only namespace, otherwise the answer is 404. The host's settings write seam is asked "+
		"before anything is changed; a refusal is 403 protected and an unreadable answer is 503. The value "+
		"is encoded under the item's declared type and then held to its declared domain, so a duration "+
		"written as \"300s\" is stored as \"5m0s\"; a value that cannot be encoded, lies outside the domain, "+
		"or exceeds the size limit answers 422. A mandatory audit row is written before the change, and if "+
		"it cannot be written the setting is left untouched and the answer is 503. The change is attributed "+
		"to the caller's accountability key, which under an impersonation is the administrator making the "+
		"request rather than the account impersonated. Success is 204 with no body.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The write's body: exactly one object, whose `value` member carries the value to store.",
		Required:    true,
	},
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The row was written"},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A layer or tenant query parameter was sent, the tenant in the path is outside its value domain, or the body is not one object"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.tenant.write in the tenant's domain, the caller has not proved an email address (contact-required), or the host's settings write seam refused the write (protected)"),
		{Status: http.StatusNotFound, Description: "The full key has no declaration, or its namespace is platform-only"},
		speedhttp.ProblemResponse(http.StatusUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusUnprocessableEntity,
			"The value cannot be encoded under the declared type or lies outside its declared domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// removeOperation declares DELETE /{tenant}/settings/:namespace/:key: it drops
// the path tenant layer's explicit row for one key. The handler builds its own
// answer because a success is 204 with no body, an undeclared or platform-only
// key is a bare 404, and a write the host's seam refuses is a 403 protected.
var removeOperation = speedhttp.NewReplyOperation[removeInput](
	"settings.tenantadmin.settings.remove",
	http.MethodDelete,
	pathNamespaceKey,
	"Drop the tenant layer's explicit row for one setting",
	"Drops the layer of the tenant in the path's explicit row for the setting named by the namespace "+
		"and key path segments. The full key `<namespace>.<key>` must match a declared item and must not "+
		"belong to a platform-only namespace, otherwise the answer is 404; deleting a row the layer does "+
		"not hold is not an error. Once the row is gone the item resolves through the fallback again. The "+
		"host's settings write seam is asked before anything is changed; a refusal is 403 protected and an "+
		"unreadable answer is 503. A mandatory audit row is written before the change, and if it cannot be "+
		"written the row is left in place and the answer is 503. The change is attributed to the caller's "+
		"accountability key, which under an impersonation is the administrator making the request rather "+
		"than the account impersonated. Success is 204 with no body.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The row was dropped"},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A layer or tenant query parameter was sent, or the tenant in the path is outside its value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.tenant.write in the tenant's domain, the caller has not proved an email address (contact-required), or the host's settings write seam refused the write (protected)"),
		{Status: http.StatusNotFound, Description: "The full key has no declaration, or its namespace is platform-only"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listOperation,
	namespaceOperation,
	setOperation,
	removeOperation,
}
