package profile

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "tenant.profile"
	fragmentVersion = "1.0.0"
)

// pathProfileFragment is the route as the fragment writes it: relative to the
// tenants-scope API root, so the absolute path is
// /api/v1/tenants/{tenant}/profile. The mounting resolves the same declaration,
// so the path is written once.
const pathProfileFragment = "/:tenant/profile"

// profileInput is the request of the read: the tenant named in the path.
type profileInput struct {
	Tenant string `path:"tenant" description:"The tenant whose organization profile the route acts on. The path-tenant layer holds it to the tenant the request resolved to before the handler runs, and the platform domain is refused."`
}

// profileWriteInput is the request of the write: the tenant named in the path
// and the four descriptive fields in the body, each of which must be present. A
// field the body carries empty clears the stored value.
type profileWriteInput struct {
	Tenant string `path:"tenant" description:"The tenant whose organization profile the route acts on. The path-tenant layer holds it to the tenant the request resolved to before the handler runs, and the platform domain is refused."`

	DisplayName  string `json:"display_name" required:"true" description:"The organization's display name, at most 100 runes. Empty clears it; a surface then falls back to the alias."`
	Description  string `json:"description" required:"true" description:"A longer account of the organization, at most 500 runes. Empty clears it."`
	ContactEmail string `json:"contact_email" required:"true" description:"The organization's contact address, one bare email address of at most 254 bytes. Empty clears it."`
	Website      string `json:"website" required:"true" description:"The organization's absolute http or https address, at most 2048 bytes, with a host and no userinfo. Empty clears it."`
}

// profileWriteFields are the body members a write must carry. The declaration
// marks them required; the entry point's binding enforces body presence and not
// member presence, so the prescreen holds the members. The names are the JSON
// names, the same snake_case the platform registration body uses.
var profileWriteFields = []string{"display_name", "description", "contact_email", "website"}

// profileBody is the 200 answer of a read and of a write: the profile view.
type profileBody = struct {
	_ struct{} `description:"The organization profile of one tenant."`

	Profile TenantProfileView `json:"profile" required:"true" description:"The tenant's organization profile, with the identifier, alias and state it is read beside."`
}

// getOperation declares GET /:tenant/profile: the organization profile of one
// tenant. Its handler builds the answer because an unknown tenant is a bare
// 404, which the problem-document writer cannot produce.
var getOperation = speedhttp.NewReplyOperation[profileInput](
	"tenant.profile.get",
	http.MethodGet,
	pathProfileFragment,
	"Read a tenant's organization profile",
	"Reads one tenant's organization profile: its identifier, alias, display name, description, contact email, "+
		"website and state, plus the time it was registered when that is known. It carries no disable source; "+
		"the platform management surface is where a disabled tenant's source is read. The caller needs "+
		"tenant.profile.read in the tenant's policy domain, and the platform domain is refused. An identifier "+
		"the roster does not hold is a 404. Reads write no audit row.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The tenant's organization profile",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(profileBody{})},
		},
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the identifier, or the path tenant is not the tenant the request resolved to or is the platform domain",
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.profile.read in the tenant's policy domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// setOperation declares PATCH /:tenant/profile: it writes one tenant's
// organization profile and nothing else. Its handler builds the answer because
// an unknown tenant is a bare 404 and a refusal carries this surface's problem
// type.
var setOperation = speedhttp.NewReplyOperation[profileWriteInput](
	"tenant.profile.update",
	http.MethodPatch,
	pathProfileFragment,
	"Write a tenant's organization profile",
	"Writes one tenant's organization profile and nothing else: the display name, description, contact email "+
		"and website. The identifier, alias, state and creation time are not written, so a profile update cannot "+
		"undo a disable or a deletion another path committed. The body carries all four fields; an empty "+
		"value clears the stored one. The acting account must have proved an email address (contact-required otherwise), and the "+
		"caller needs tenant.profile.write in the tenant's policy domain. A profile outside the stated bounds is a "+
		"400, an unknown tenant a 404, a tombstone a 409, and the platform domain is refused. A mandatory audit row "+
		"lands before the roster write; if it cannot, nothing is written and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The four descriptive fields, all of them present. An empty value clears the stored one.",
		Required:    true,
		Name:        "TenantProfileWrite",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The profile as written",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(profileBody{})},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The profile is outside the stated bounds, or the request body is not one JSON object"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the identifier, or the path tenant is not the tenant the request resolved to or is the platform domain",
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.profile.write in the tenant's policy domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The tenant is a tombstone; a deleted tenant's profile is terminal"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	getOperation,
	setOperation,
}
