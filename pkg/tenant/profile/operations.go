package profile

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "tenant.profile"
	fragmentVersion = "1.0.0"
)

const pathProfileFragment = "/:tenant/profile"

type profileInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the organization profile belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`
}

type profileWriteInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the organization profile belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`

	DisplayName  string `json:"display_name" required:"true" description:"The organization's display name, at most 100 runes. Empty clears it; a surface then falls back to the name."`
	Description  string `json:"description" required:"true" description:"A longer account of the organization, at most 500 runes. Empty clears it."`
	ContactEmail string `json:"contact_email" required:"true" description:"The organization's contact address, one bare email address of at most 254 bytes. Empty clears it."`
	Website      string `json:"website" required:"true" description:"The organization's absolute http or https address, at most 2048 bytes, with a host and no userinfo. Empty clears it."`
}

var profileWriteFields = []string{"display_name", "description", "contact_email", "website"}

type profileBody = struct {
	_ struct{} `description:"The organization profile of one tenant."`

	Profile TenantProfileView `json:"profile" required:"true" description:"The tenant's organization profile, with its name, kind, owner account and state."`
}

var getOperation = speedhttp.NewReplyOperation[profileInput](
	"tenant.profile.get",
	http.MethodGet,
	pathProfileFragment,
	"Read a tenant's organization profile",
	"Reads one tenant's organization profile: its name, kind, display name, description, contact email, "+
		"website, state and owner account, plus the time it was registered when that is known. It carries no disable source; "+
		"the platform management surface is where a disabled tenant's source is read. The caller needs "+
		"tenant.profile.read in the tenant's policy domain, and the platform domain is refused. A name "+
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

var setOperation = speedhttp.NewReplyOperation[profileWriteInput](
	"tenant.profile.update",
	http.MethodPatch,
	pathProfileFragment,
	"Write a tenant's organization profile",
	"Writes one tenant's organization profile and nothing else: the display name, description, contact email "+
		"and website. The name, kind, state and creation time are not written, so a profile update cannot "+
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

var operations = speedhttp.Operations{
	getOperation,
	setOperation,
}
