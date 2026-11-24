package selfcreate

import (
	_ "embed"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const pathFragment = "/orgs"

const (
	problemPrefix          = "urn:speed:org:"
	creationInProgressCode = "creation-in-progress"
	organizationLimitCode  = "organization-limit"
	rateLimitedCode        = "rate-limited"
	identifierUnavailable  = "identifier-unavailable"
)

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

var spec = speedhttp.Spec{
	Endpoint: endpointName,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

// RoutePath is the absolute path the creation is mounted on.
var RoutePath = spec.ResolvePath(pathFragment)

type createInput struct {
	_ struct{} `description:"The name, display name and description of the organization to create. The identifier is generated."`

	Name        string `json:"name" required:"true" description:"The organization's public name: a lower-case slug of 3 to 63 letters, digits and dashes, beginning with a letter or a digit. It is unique across the roster and cannot be changed after creation."`
	DisplayName string `json:"display_name" required:"true" description:"The organization's display name; 1 to 100 runes after trimming, and the surface requires it non-empty."`
	Description string `json:"description" description:"The organization's description; at most 500 runes after trimming."`
}

// OrganizationCreatedReply is the creation's success answer.
type OrganizationCreatedReply struct {
	_ struct{} `description:"The organization this call created."`

	Tenant OrganizationCreatedTenant `json:"tenant" required:"true"`
}

// OrganizationCreatedTenant is the created organization as the answer renders
// it; the identifier stays internal.
type OrganizationCreatedTenant struct {
	_ struct{} `description:"The created organization: the caller's name and profile, and the active state. The generated identifier is internal and never appears."`

	Name        string `json:"name" required:"true" description:"The request-surface name the caller chose; unique across the roster and immutable after creation."`
	DisplayName string `json:"display_name" required:"true" description:"The organization's display name, trimmed."`
	Description string `json:"description" required:"true" description:"The organization's description, trimmed."`
	Status      string `json:"status" required:"true" enum:"active" description:"The organization's state; a self-service organization starts active."`
}

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

var operations = speedhttp.Operations{createOperation}
