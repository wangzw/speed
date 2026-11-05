package personal

import (
	"context"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

var _ tenant.KindResolver = (*product)(nil)

// KindOf implements tenant.KindResolver: the calling account's own personal
// tenant is personal, and every other tenant is business.
//
// The answer compares the row's identifier with the one the caller's account
// derives, exactly. A prefix test would mark another account's personal tenant
// personal too, and that row can appear in the caller's membership list; the
// identity comes off the request context.
func (p *product) KindOf(ctx context.Context, id tenant.ID) (tenant.Kind, error) {
	identity, ok := authn.FromContext(ctx)
	if !ok {
		return tenant.KindBusiness, nil
	}
	if id == TenantID(identity.Account()) {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}

// OrganizationPersonalReply is the ensure's success answer.
type OrganizationPersonalReply struct {
	_ struct{} `description:"The answer to a successful ensure: the caller's personal tenant and whether this call registered it."`

	Tenant  OrganizationPersonalTenant `json:"tenant" required:"true"`
	Created bool                       `json:"created" required:"true" description:"Whether this call registered the tenant"`
}

// OrganizationPersonalTenant is the tenant the answer names.
type OrganizationPersonalTenant struct {
	_ struct{} `description:"The personal tenant the ensure names."`

	ID    string `json:"id" required:"true" description:"The tenant identifier, derived deterministically from the account identifier: the fixed prefix \"personal-\" followed by the account identifier. The scoped tables hold it, so it takes no phone number, display name or other personal data, and it does not change with the request language"`
	Alias string `json:"alias" required:"true" description:"The request-surface name. It is the account's username, or the derived identifier when the account carries no username"`
	Kind  string `json:"kind" required:"true" enum:"personal" description:"The tenant kind; the frontend renders the personal organization label from it"`
}

// personalEnsureOperation declares the ensure route and binds its types.
var personalEnsureOperation = speedhttp.NewOperation[struct{}, OrganizationPersonalReply](
	"org.personal.ensure",
	http.MethodPost,
	personalPathFragment,
	"Ensure the caller's personal tenant exists and is complete",
	"Backfills the caller's personal tenant when the account's own membership is missing, and reads it "+
		"back otherwise. Every call completes the tenant's tenant-admin role and the caller's binding and "+
		"converges the role's grants to the module's allowlist. A membership row that exists is left as it "+
		"stands, and a tenant that is not enabled is refused without a write. The tenant identifier is "+
		"derived from the account identifier, and the alias is the account's username; the answer names "+
		"the tenant and whether this call created it.",
	nil,
	[]speedhttp.Response{
		{
			Status:      200,
			Description: "The personal tenant, complete; created says whether this call registered it",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is a service account, or a static-table subject that "+
				"cannot own a tenant"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The caller cannot have a personal tenant: its personal tenant exists and is not enabled "+
				"(tenant-unavailable), or the derived identifier or alias is unavailable "+
				"(identifier-unavailable). The type field names which. tenant-unavailable changes if an "+
				"operator enables the tenant, and identifier-unavailable does not change on retry"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"A dependency could not answer; no part of the result is returned"),
	},
)

// personalOperations is this module's declaration of the API it serves.
var personalOperations = speedhttp.Operations{personalEnsureOperation}
