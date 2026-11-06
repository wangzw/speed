package tenantadmin

import (
	"net/http"

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
// appears in a path. There is no per-namespace read: the listing is the one read
// route, and an unknown path is answered by the router before any setting is
// looked at.
const (
	pathSettings     = "/:tenant/settings"
	pathNamespaceKey = pathSettings + "/:namespace/:key"
)

// The closed set of values a view's source takes: the layer the effective value
// points at. A platform policy that is on is global; otherwise a tenant row is
// tenant; otherwise a global row is global; otherwise the declared default is the
// value and the source is default.
const (
	sourceTenant  = "tenant"
	sourceGlobal  = "global"
	sourceDefault = "default"
)

// SettingsTenantAdminSettings is the tenant management surface's read answer: the
// items the declarations expose to a tenant, and whether this tenant's settings
// may be written at all.
//
// writable is the host's answer for the tenant, not for an item: a protected
// tenant's surface is read-only as a whole, and read_only_reason names why in
// the closed set this capability uses: `protected` when the seam refuses the
// tenant and `unavailable` when the seam cannot answer. Both answers keep
// writable false; the listing still answers 200, because the settings themselves
// were read and only the write claim is withheld. An empty read_only_reason with
// writable true means the surface accepts writes. A platform-enforced policy is
// a per-item lock, reported on the item as locked_by_platform, and does not make
// the surface read-only.
type SettingsTenantAdminSettings struct {
	_ struct{} `description:"The tenant settings surface's read answer: the declared items it exposes and whether the tenant's settings may be written at all."`

	Settings       []SettingsTenantAdminSetting `json:"settings" required:"true" description:"The items whose declarations expose them to a tenant, each with its platform value, tenant value and effective value."`
	Writable       bool                         `json:"writable" required:"true" description:"Whether this tenant's settings may be written through this surface. False when the host's settings write seam refuses the tenant, in which case every write route answers 403 protected, and when the seam cannot answer, in which case every write route answers 503."`
	ReadOnlyReason string                       `json:"read_only_reason" required:"true" description:"Why the surface is read-only, empty when writable is true. The closed set is the refusal code this capability writes for the same condition: \"protected\" when the host's settings write seam refuses the tenant, \"unavailable\" when the seam cannot answer. The listing still answers 200 with the settings it read in both cases."`
}

// SettingsTenantAdminSetting is one declared setting item as the tenant
// management surface shows it. It is built field by field from a declaration and
// the rows of the two layers and never handed through as it stands, so that a
// field added to a declaration later does not reach a client by being there.
//
// The value-bearing fields are text in the declared type's own form. platform_value
// is the global row's text or the declared default when the global layer holds
// no row; tenant_value is the tenant row's text or the declared default when the
// tenant holds no row; effective_value is the boolean OR of the two, the policy a
// platform floor cannot lower. A sensitive item's three value fields carry the
// settings capability's mask string, while has_row and source still answer.
//
// locked_by_platform says the platform layer holds the item on, so a tenant write
// that would turn it off is refused with 409. source names the layer the effective
// value points at, in the existing closed set tenant, global and default; a
// platform-enforced item is global.
type SettingsTenantAdminSetting struct {
	_ struct{} `description:"One declared setting item as the tenant management surface shows it. The declaration fields and the item's control metadata come from the key table; the three value fields come from the tenant layer's rows and the global layer's rows, and every value-bearing field is text in the declared type's own form."`

	Key              string `json:"key" required:"true" description:"The full setting key: the declaring module's namespace joined with the item's own segment by a dot."`
	Title            string `json:"title" required:"true" description:"The item's title, rendered in the request's language from the declaration's key, falling back to the declared English text when no localizer is assembled."`
	Description      string `json:"description" required:"true" description:"The item's description, rendered in the request's language from the declaration's key, falling back to the declared English text when no localizer is assembled."`
	Type             string `json:"type" required:"true" description:"The declared type's name as reflection reports it. The tenant surface renders switches, so this is \"bool\"."`
	Control          string `json:"control" required:"true" enum:"switch" description:"The control the surface renders for the item."`
	Danger           string `json:"danger" required:"true" description:"How destructive turning the item off is: \"critical\" when the surface must confirm the change first, empty when there is no such warning."`
	Effect           string `json:"effect" required:"true" description:"When a change to the item takes effect: \"immediate\" when it applies as soon as it is saved, empty when the declaration names none."`
	TenantAccess     string `json:"tenant_access" required:"true" enum:"read,read-write" description:"How much of the item the tenant surface exposes: \"read\" for a visible but unwritable item, \"read-write\" for one the tenant may change."`
	EffectiveValue   string `json:"effective_value" required:"true" description:"The text the item resolves to: the platform policy OR the tenant policy, in the declared type's text form. The mask string for a sensitive item."`
	PlatformValue    string `json:"platform_value" required:"true" description:"The global layer's value in the declared type's text form, the declared default when the global layer holds no row. The mask string for a sensitive item."`
	TenantValue      string `json:"tenant_value" required:"true" description:"The tenant layer's value in the declared type's text form, the declared default when the tenant holds no row. The mask string for a sensitive item."`
	LockedByPlatform bool   `json:"locked_by_platform" required:"true" description:"Whether the platform layer holds this policy on, so that a tenant write turning it off answers 409 tenant-admission-enforced."`
	HasRow           bool   `json:"has_row" required:"true" description:"Whether the tenant layer holds an explicit row for the item, whether or not the platform policy is forcing the effective value."`
	Source           string `json:"source" required:"true" enum:"tenant,global,default" description:"The layer the effective value comes from: \"global\" when the platform policy is on or the global layer holds a row, \"tenant\" when the tenant holds a row and the platform is not forcing the item, \"default\" when neither holds a row."`
}

// listInput is the input of the all-items read: the tenant the path names.
type listInput struct {
	Tenant string `path:"tenant" required:"true" description:"The tenant's alias named in the path. The path-tenant layer resolves it to the internal identifier before the handler runs; the layer and the policy domain are both fixed to that tenant, and a \"layer\" or \"tenant\" query parameter is refused with 400."`
}

// setInput is the input of the write route: the tenant, the two path segments of
// the full key, and the JSON body carrying the value. The body is bound by the
// entry point, which refuses a member the declaration does not carry.
type setInput struct {
	Tenant    string `path:"tenant" required:"true" description:"The tenant's alias named in the path. The path-tenant layer resolves it to the internal identifier before the handler runs; the write lands on that tenant's settings layer, and a \"layer\" or \"tenant\" query parameter is refused with 400."`
	Namespace string `path:"namespace" description:"The namespace of the item to write: the declaring module, which is the key's leading segment. Joined with \"key\" by a dot to form the full key, which must be declared with read-write tenant access."`
	Key       string `path:"key" description:"The rest of the setting key under the namespace; dots are allowed. Joined with the namespace by a dot to form the full key, which must be declared with read-write tenant access."`
	Value     any    `json:"value" required:"true" description:"The value to store, in the JSON form the item's declared type is read from: a boolean for a switch. It is encoded under the declared type and then held to the item's declared domain; a value that cannot be encoded, lies outside the domain, or exceeds the size limit answers 422. The mask string a sensitive item is shown as is refused with 422 masked-value before anything runs."`
}

// removeInput is the input of the delete route: the tenant and the two path
// segments of the full key.
type removeInput struct {
	Tenant    string `path:"tenant" required:"true" description:"The tenant's alias named in the path. The path-tenant layer resolves it to the internal identifier before the handler runs; the row is dropped from that tenant's settings layer, and a \"layer\" or \"tenant\" query parameter is refused with 400."`
	Namespace string `path:"namespace" description:"The namespace of the item whose row is dropped: the declaring module, which is the key's leading segment. Joined with \"key\" by a dot to form the full key, which must be declared with read-write tenant access."`
	Key       string `path:"key" description:"The rest of the setting key under the namespace; dots are allowed. Joined with the namespace by a dot to form the full key, which must be declared with read-write tenant access."`
}

// listOperation declares GET /{tenant}/settings: the items the declarations
// expose to a tenant, in the envelope that also says whether the surface is
// writable. The handler returns one success body, so this is the simple form.
var listOperation = speedhttp.NewOperation[listInput, SettingsTenantAdminSettings](
	"settings.tenantadmin.settings.list",
	http.MethodGet,
	pathSettings,
	"List the declared settings the tenant surface exposes",
	"Returns the items whose declarations expose them to a tenant: a schema that declares itself tenant-facing and an item that states a tenant access. No namespace is included or excluded by its name. Each item carries the platform layer's value, the tenant layer's value and their boolean OR, its control metadata, whether the platform layer locks it, whether the tenant layer holds a row, and the layer the effective value comes from. Titles and descriptions are rendered in the request's language. The envelope also says whether the host's settings write seam would accept a write to this tenant. A failure to read either layer, or to ask the seam, answers 503 rather than leaving an item out.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The items the declarations expose to a tenant",
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

// setOperation declares PUT /{tenant}/settings/:namespace/:key: it writes one
// row on the path tenant's layer. The handler builds its own answer because a
// success is 204 with no body, a key no declaration exposes to a tenant is a
// bare 404, a read-only item is a 403 read-only, a write the host's seam refuses
// is a 403 protected, a disable of a platform-enforced policy is a 409, and a
// masked value is 422.
var setOperation = speedhttp.NewReplyOperation[setInput](
	"settings.tenantadmin.settings.set",
	http.MethodPut,
	pathNamespaceKey,
	"Write one setting row on the tenant layer",
	"Writes one row for the setting named by the namespace and key path segments on the layer of the "+
		"tenant in the path. The full key `<namespace>.<key>` must match a declared item the tenant "+
		"surface exposes. A key with no tenant access, whether undeclared or declared on the platform "+
		"surface alone, is 404 — the same answer an undeclared key gets. An item declared read-only is 403 read-only: it is "+
		"visible, and the refusal names why the write cannot be made rather than hiding the item. A "+
		"value of the mask string a sensitive item is shown as is 422 masked-value before the seam, the "+
		"audit and the store. A write turning a boolean switch off while the platform layer holds it on "+
		"is 409 tenant-admission-enforced, also before the seam, the audit and the store. The host's "+
		"settings write seam is asked before anything is changed; a refusal is 403 protected and an "+
		"unreadable answer is 503. The value is encoded under the item's declared type and then held to "+
		"its declared domain; a value that cannot be encoded, lies outside the domain, or exceeds the "+
		"size limit answers 422. A mandatory audit row is written before the change, and if it cannot be "+
		"written the setting is left untouched and the answer is 503. The change is attributed to the "+
		"caller's accountability key, which under an impersonation is the administrator making the "+
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
			"The caller holds no settings.tenant.write in the tenant's domain, the caller has not proved an email address (contact-required), the item is declared read-only (read-only), or the host's settings write seam refused the write (protected)"),
		{Status: http.StatusNotFound, Description: "The full key is not declared or exposes no tenant access"},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The write would turn off a policy the platform layer requires (tenant-admission-enforced)"),
		speedhttp.ProblemResponse(http.StatusUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusUnprocessableEntity,
			"The value is the mask string a sensitive item is shown as (masked-value), cannot be encoded under the declared type, or lies outside its declared domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// removeOperation declares DELETE /{tenant}/settings/:namespace/:key: it drops
// the path tenant layer's explicit row for one key. The handler builds its own
// answer because a success is 204 with no body, a key no declaration exposes to a
// tenant is a bare 404, a read-only item is a 403 read-only, and a write the
// host's seam refuses is a 403 protected.
var removeOperation = speedhttp.NewReplyOperation[removeInput](
	"settings.tenantadmin.settings.remove",
	http.MethodDelete,
	pathNamespaceKey,
	"Drop the tenant layer's explicit row for one setting",
	"Drops the layer of the tenant in the path's explicit row for the setting named by the namespace "+
		"and key path segments. The full key `<namespace>.<key>` must match a declared item the tenant "+
		"surface exposes. A key with no tenant access, whether undeclared or declared on the platform "+
		"surface alone, is 404 — the same answer an undeclared key gets. An item declared read-only is 403 read-only: it is "+
		"visible, and the refusal names why the row cannot be dropped rather than hiding the item. "+
		"Deleting a row the layer does not hold is not an error. Dropping the tenant row while the "+
		"platform layer holds the policy on is allowed: the effective value stays on, so there is no "+
		"disable to refuse. The host's settings write seam is asked before anything is changed; a "+
		"refusal is 403 protected and an unreadable answer is 503. A mandatory audit row is written "+
		"before the change, and if it cannot be written the row is left in place and the answer is 503. "+
		"The change is attributed to the caller's accountability key, which under an impersonation is the "+
		"administrator making the request rather than the account impersonated. Success is 204 with no "+
		"body.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The row was dropped"},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A layer or tenant query parameter was sent, or the tenant in the path is outside its value domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no settings.tenant.write in the tenant's domain, the caller has not proved an email address (contact-required), the item is declared read-only (read-only), or the host's settings write seam refused the write (protected)"),
		{Status: http.StatusNotFound, Description: "The full key is not declared or exposes no tenant access"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listOperation,
	setOperation,
	removeOperation,
}
