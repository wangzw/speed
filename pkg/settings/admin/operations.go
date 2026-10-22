package admin

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title is the module's name and the version
// is the version of the fragment's shape.
const (
	fragmentTitle   = "settings.admin"
	fragmentVersion = "1.0.0"
)

// The management routes, relative to the API root the module's Spec declares:
// the routes are mounted on their own base path and not under a configurable
// one, and every one of them is addressed by a natural key — a setting key's
// namespace and the rest of the key — so that no opaque row identifier ever
// appears in a path.
const (
	pathSettings     = "/settings"
	pathNamespace    = pathSettings + "/:namespace"
	pathNamespaceKey = pathSettings + "/:namespace/:key"
)

// SettingsAdminSetting is one declared setting item as the management routes
// show it. It is built field by field from a declaration and the resolved value
// and never handed through as it stands, so that a field added to a declaration
// later does not reach a client by being there.
//
// Every value-bearing field is text: default is the declared default in the
// declared type's own text form, written is the text the selected layer holds,
// and value is the text the item resolves to after the three-level fallback —
// tenant row, global row, declared default. They share one form, so a client
// compares and displays them without converting between a typed value and a row,
// and a duration reads "5m0s" rather than as a count of nanoseconds. A sensitive
// item's default is empty, and its written and value carry the mask string
// instead of the value.
//
// written alone cannot say whether the layer holds a row: an empty text is both
// a missing row and a row written as the empty string. has_row carries that fact
// on its own, so a client can mark the item as the declared default or as an
// override without interpreting the text, and a sensitive item tells a client
// whether there is a row to remove even though its text is masked.
type SettingsAdminSetting struct {
	_ struct{} `description:"One declared setting item as this management surface shows it, on the selected layer. The declaration fields come from the key table; written, has_row and value come from that layer's rows and from the resolved read. Every value-bearing field is text in the declared type's own form, so a duration reads as \"5m0s\" rather than as a count of nanoseconds."`

	Key         string `json:"key" required:"true" description:"The full setting key: the declaring module's namespace joined with the item's own segment by a dot."`
	Namespace   string `json:"namespace" required:"true" description:"The module that declared the item"`
	Type        string `json:"type" required:"true" description:"The declared type's name as reflection reports it, such as \"string\", \"int\", \"bool\" or \"time.Duration\"; the item's values are stored and rendered in that type's text form."`
	Description string `json:"description" required:"true" description:"The human-readable text the declaring module gave the item."`
	Domain      string `json:"domain" required:"true" description:"The declared rule over the item's values, empty when every value of the type is admitted"`
	Sensitive   bool   `json:"sensitive" required:"true" description:"Whether the item is a credential. A sensitive item's default is empty and its written and value carry a fixed mask string; has_row still reports whether the layer holds a row."`
	Default     string `json:"default" required:"true" description:"The declared default in the declared type's text form, empty for a sensitive item"`
	Written     string `json:"written" required:"true" description:"The text the selected layer holds, the mask string for a sensitive item"`
	HasRow      bool   `json:"has_row" required:"true" description:"Whether the selected layer holds a row for the item. For a non-sensitive item, written is empty both when no row is held and when the row's text is empty; a sensitive item's written is the mask string whether or not a row exists."`
	Value       string `json:"value" required:"true" description:"The text the item resolves to after the three-level fallback, the mask string for a sensitive item"`
}

// listInput is the input of the all-items read: the layer selection. The
// handler rebuilds the settled selection from these fields; the authorization
// step in front of it has already validated them, so they are not judged again.
type listInput struct {
	Layer  string `query:"layer" required:"true" enum:"global,tenant" description:"The layer the listing reads: \"global\" for the platform layer, or \"tenant\" for the layer of the tenant named by \"tenant\". There is no default; an absent value or one outside this set is a 400."`
	Tenant string `query:"tenant" description:"The tenant whose layer the listing reads. Required when \"layer\" is \"tenant\" and refused when it is \"global\"; an empty identifier or one containing blanks or control characters is a 400."`
}

// namespaceInput is the input of the per-namespace read: the layer selection and
// the namespace path segment.
type namespaceInput struct {
	Layer     string `query:"layer" required:"true" enum:"global,tenant" description:"The layer the listing reads: \"global\" for the platform layer, or \"tenant\" for the layer of the tenant named by \"tenant\". There is no default; an absent value or one outside this set is a 400."`
	Tenant    string `query:"tenant" description:"The tenant whose layer the listing reads. Required when \"layer\" is \"tenant\" and refused when it is \"global\"; an empty identifier or one containing blanks or control characters is a 400."`
	Namespace string `path:"namespace" description:"The namespace whose declared items the listing returns: the declaring module, which is the key's leading segment (for example \"deals\" or \"authn.session\"). A namespace no declared item belongs to is a 404."`
}

// setInput is the input of the write route: the layer selection, the two path
// segments of the full key, and the JSON body carrying the value. The body is
// bound by the entry point, which refuses a member the declaration does not
// carry.
type setInput struct {
	Layer     string `query:"layer" required:"true" enum:"global,tenant" description:"The layer the write lands on: \"global\" for the platform layer, or \"tenant\" for the layer of the tenant named by \"tenant\". There is no default; an absent value or one outside this set is a 400."`
	Tenant    string `query:"tenant" description:"The tenant whose layer the write lands on. Required when \"layer\" is \"tenant\" and refused when it is \"global\"; an empty identifier or one containing blanks or control characters is a 400."`
	Namespace string `path:"namespace" description:"The namespace of the item to write: the declaring module, which is the key's leading segment. Joined with \"key\" by a dot to form the full key, which must be declared."`
	Key       string `path:"key" description:"The rest of the setting key under the namespace; dots are allowed. Joined with the namespace by a dot to form the full key, which must be declared."`
	Value     any    `json:"value" required:"true" description:"The value to store, in the JSON form the item's declared type is read from: a string for a string, duration, or text-decoding item, a number for a numeric item, a boolean for a bool item. It is encoded under the declared type and then held to the item's declared domain; a value that cannot be encoded, lies outside the domain, or exceeds the size limit answers 422."`
}

// removeInput is the input of the delete route: the layer selection and the two
// path segments of the full key.
type removeInput struct {
	Layer     string `query:"layer" required:"true" enum:"global,tenant" description:"The layer whose row is dropped: \"global\" for the platform layer, or \"tenant\" for the layer of the tenant named by \"tenant\". There is no default; an absent value or one outside this set is a 400."`
	Tenant    string `query:"tenant" description:"The tenant whose layer the row is dropped from. Required when \"layer\" is \"tenant\" and refused when it is \"global\"; an empty identifier or one containing blanks or control characters is a 400."`
	Namespace string `path:"namespace" description:"The namespace of the item whose row is dropped: the declaring module, which is the key's leading segment. Joined with \"key\" by a dot to form the full key, which must be declared."`
	Key       string `path:"key" description:"The rest of the setting key under the namespace; dots are allowed. Joined with the namespace by a dot to form the full key, which must be declared."`
}

// settingListType is the success body's type: the declared items, each as the
// management surface shows it.
var settingListType = reflect.TypeOf([]SettingsAdminSetting{})

// listOperation declares GET /settings: every declared item with
// the value it resolves to on the selected layer. The handler returns one
// success body, so this is the simple form.
var listOperation = speedhttp.NewOperation[listInput, []SettingsAdminSetting](
	"settings.admin.settings.list",
	http.MethodGet,
	pathSettings,
	"List every declared setting with its value on the selected layer",
	"Returns every item in the declaration listing, including keys declared by modules that do not run "+
		"in this process, each with the value it resolves to on the selected layer. Resolution walks the "+
		"three-level fallback starting at that layer: the tenant row, then the global row, then the "+
		"declared default. Every value-bearing field is text in the declared type's own form; for a "+
		"sensitive item default is empty and written and value carry a fixed mask string. A failure to "+
		"read the layer's rows or to resolve a value answers 503 rather than leaving the item out.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every declared item",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The layer selection is unreadable, or the tenant identifier is outside its value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.read in the layer's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// namespaceOperation declares GET /settings/:namespace: the
// declared items one namespace declares. The handler builds its own answer
// because a namespace no declared item belongs to is a bare 404, which the
// simple form cannot write.
var namespaceOperation = speedhttp.NewReplyOperation[namespaceInput](
	"settings.admin.settings.listNamespace",
	http.MethodGet,
	pathNamespace,
	"List the declared settings one namespace declares",
	"Returns the declared items whose namespace equals the path segment, that is the group one module "+
		"declared, each carrying the same fields and resolved the same way as on the all-items route. "+
		"The declaration listing is not narrowed to the modules running in this process. A namespace no "+
		"declared item belongs to answers 404 without a problem document.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The namespace's declared items",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: settingListType},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The layer selection is unreadable, or the tenant identifier is outside its value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.read in the layer's domain"),
		{Status: http.StatusNotFound, Description: "No declared item belongs to the namespace"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// setOperation declares PUT /settings/:namespace/:key: it writes
// one row on the selected layer. The handler builds its own answer because a
// success is 204 with no body and an undeclared key is a bare 404.
var setOperation = speedhttp.NewReplyOperation[setInput](
	"settings.admin.settings.set",
	http.MethodPut,
	pathNamespaceKey,
	"Write one setting row on the selected layer",
	"Writes one row for the setting named by the namespace and key path segments on the selected layer. "+
		"The full key `<namespace>.<key>` must match a declared item, otherwise the answer is 404. The "+
		"value is encoded under the item's declared type and then held to its declared domain, so a "+
		"duration written as \"300s\" is stored as \"5m0s\"; a value that cannot be encoded, lies outside "+
		"the domain, or exceeds the size limit answers 422. A mandatory audit row is written before the "+
		"change, and if it cannot be written the setting is left untouched and the answer is 503. The "+
		"change is attributed to the caller's accountability key, which under an impersonation is the "+
		"administrator making the request rather than the account impersonated. Success is 204 with no "+
		"body.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The write's body: exactly one object, whose `value` member carries the value to store.",
		Required:    true,
	},
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The row was written"},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The layer selection is unreadable, the tenant identifier is outside its value domain, or the body is not one object"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.write in the layer's domain, or the caller has not proved an email address (contact-required)"),
		{Status: http.StatusNotFound, Description: "The full key has no declaration"},
		speedhttp.ProblemResponse(http.StatusUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusUnprocessableEntity,
			"The value cannot be encoded under the declared type or lies outside its declared domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// removeOperation declares DELETE /settings/:namespace/:key: it
// drops the selected layer's explicit row for one key. The handler builds its
// own answer because a success is 204 with no body and an undeclared key is a
// bare 404.
var removeOperation = speedhttp.NewReplyOperation[removeInput](
	"settings.admin.settings.remove",
	http.MethodDelete,
	pathNamespaceKey,
	"Drop the selected layer's explicit row for one setting",
	"Drops the selected layer's explicit row for the setting named by the namespace and key path "+
		"segments. The full key `<namespace>.<key>` must match a declared item, otherwise the answer is "+
		"404; deleting a row the layer does not hold is not an error. Once the row is gone the item "+
		"resolves through the fallback again. A mandatory audit row is written before the change, and if "+
		"it cannot be written the row is left in place and the answer is 503. The change is attributed to "+
		"the caller's accountability key, which under an impersonation is the administrator making the "+
		"request rather than the account impersonated. Success is 204 with no body.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The row was dropped"},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The layer selection is unreadable, or the tenant identifier is outside its value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.write in the layer's domain, or the caller has not proved an email address (contact-required)"),
		{Status: http.StatusNotFound, Description: "The full key has no declaration"},
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
