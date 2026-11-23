package selfcreate

import (
	_ "embed"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// pathFragment is the route as the fragment writes it: relative to the API root
// the module's spec declares.
const pathFragment = "/orgs"

// The problem types the module writes, in the org namespace the personal
// tenant's refusals also use. A code the frontend renders by code has to keep
// the urn:speed:<module>:<dashed-code> shape, because @speed/api-client
// normalizes each dash to an underscore when it names the i18n key.
const (
	problemPrefix          = "urn:speed:org:"
	creationInProgressCode = "creation-in-progress"
	organizationLimitCode  = "organization-limit"
	rateLimitedCode        = "rate-limited"
	identifierUnavailable  = "identifier-unavailable"
)

// The audit vocabulary the module writes.
const (
	auditAction     = "org.tenant.create"
	outcomeSuccess  = "success"
	outcomeFailed   = "failed"
	detailActionID  = "action_id"
	detailAccount   = "account"
	detailTenant    = "tenant"
	fragmentTitle   = "organization create"
	fragmentVersion = "1.0.0"
)

//go:embed openapi/tenant.selfcreate.json
var openAPIFragment []byte

// spec is the API root the module's fragment and route hang off. The route
// hangs off the account endpoint's /api/v1/orgs claim; the prefix is written
// into every configuration, so it stays claimed even when the switch is off
// (the claim is configuration, the mounting is assembly: an off switch answers
// the path with the endpoint's own 404).
var spec = speedhttp.Spec{
	Endpoint: endpointName,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

// RoutePath is the absolute path the creation is mounted on: the fragment path
// resolved against the API root.
var RoutePath = spec.ResolvePath(pathFragment)

// createInput is the request body of the self-service creation. The caller
// chooses the name and gives a display name and a description; the identifier
// is generated, so no technical name reaches a person filling the form.
type createInput struct {
	_ struct{} `description:"The name, display name and description of the organization to create. The identifier is generated."`

	Name        string `json:"name" required:"true" description:"The organization's public name: a lower-case slug of 3 to 63 letters, digits and dashes, beginning with a letter or a digit. It is unique across the roster and cannot be changed after creation."`
	DisplayName string `json:"display_name" required:"true" description:"The organization's display name; 1 to 100 runes after trimming, and the surface requires it non-empty."`
	Description string `json:"description" description:"The organization's description; at most 500 runes after trimming."`
}

// OrganizationCreatedReply is the creation's success answer.
type OrganizationCreatedReply struct {
	_ struct{} `description:"The organization this call created."`

	// Tenant is the organization this call created, rendered as the answer shows
	// it.
	Tenant OrganizationCreatedTenant `json:"tenant" required:"true"`
}

// OrganizationCreatedTenant is the created organization as the answer renders
// it. The identifier is generated and stays internal; the name is the one the
// caller chose.
type OrganizationCreatedTenant struct {
	_ struct{} `description:"The created organization: the caller's name and profile, and the active state. The generated identifier is internal and never appears."`

	// Name is the caller's chosen name; it is unique across the roster and
	// immutable after creation.
	Name string `json:"name" required:"true" description:"The request-surface name the caller chose; unique across the roster and immutable after creation."`
	// DisplayName is the display name as stored, trimmed of surrounding space.
	DisplayName string `json:"display_name" required:"true" description:"The organization's display name, trimmed."`
	// Description is the description as stored, trimmed of surrounding space.
	Description string `json:"description" required:"true" description:"The organization's description, trimmed."`
	// Status is always active on this answer; a self-service organization starts
	// active.
	Status string `json:"status" required:"true" enum:"active" description:"The organization's state; a self-service organization starts active."`
}

// rateLimitedResponse is the 429 answer with the Retry-After header declared. A
// handler's Fault supplies the value; the declaration publishes the header on
// the contract.
func rateLimitedResponse() speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusTooManyRequests,
		"The account has reached the hourly creation limit; Retry-After names the seconds until the next window")
	response.Headers = []speedhttp.Header{{
		Name:        "Retry-After",
		Description: "The number of seconds until the hourly window opens again",
		Value:       "1",
	}}
	return response
}

// createOperation declares POST /orgs: the creation of one business
// organization for the calling account.
var createOperation = speedhttp.NewOperation[createInput, OrganizationCreatedReply](
	"org.create.create",
	http.MethodPost,
	pathFragment,
	"Create a business organization",
	"Creates one active business organization for the calling account through the provisioning seam: the "+
		"caller becomes its first tenant-admin, the tenant-admin role is marked builtin and carries the governed "+
		"permission set. The caller chooses the name; the identifier is generated and stays internal. A pending "+
		"email address is refused (contact-required); an impersonating caller, a service account and a static "+
		"subject are refused. The name is held to the roster's slug form, the reserved-name set and one "+
		"namespace with usernames and tenant identifiers. A per-account lock, the active-business-organization "+
		"cap and the hourly creation limit bound the call, and a mandatory audit row must land before the "+
		"provisioner runs.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The name, display name and description of the organization; the identifier is generated.",
		Required:    true,
		Name:        "OrganizationCreateRequest",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusCreated,
			Description: "The organization as created",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name or display name is missing or outside its bounds, or the name is reserved"),
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is impersonating, is a service account or a static subject, or has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The caller cannot create an organization now: a creation is already in progress "+
				"(creation-in-progress), the account holds the maximum number of active business "+
				"organizations (organization-limit), or the name or generated identifier is unavailable "+
				"(identifier-unavailable). The type field names which"),
		rateLimitedResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"A dependency could not answer; no part of the organization is created"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{createOperation}
