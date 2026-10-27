package admin

import (
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn admin"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the module's API root.
// The mounter registration resolves the same declaration against its base, so
// each path is written once.
const (
	// System scope: the platform-domain account and session surfaces, and the
	// platform's tenant-addressed impersonation-record group. The tenant in an
	// impersonation path is the data the records are confined to, not the policy
	// domain the decision is made in.
	pathAccounts        = "/auth/accounts"
	pathAccount         = "/auth/accounts/:id"
	pathIdentities      = pathAccount + "/identifiers"
	pathIdentity        = pathIdentities + "/:identifier"
	pathStatus          = pathAccount + "/status"
	pathPassword        = pathAccount + "/password"
	pathFactors         = pathAccount + "/factors"
	pathMFAFlag         = pathAccount + "/mfa-required"
	pathMFAReset        = pathAccount + "/mfa-reset"
	pathAccountSessions = pathAccount + "/sessions"
	pathRevokeSessions  = pathAccount + "/revoke-sessions"
	pathSession         = "/auth/sessions/:id"
	pathSecrets         = pathAccount + "/secrets"
	pathSecret          = pathSecrets + "/:secret"
	pathImpersonations  = "/tenants/:tenant/impersonations"
	pathImpersonation   = pathImpersonations + "/:id"

	// Tenant-prefixed scope: the tenant is the first segment, so the level and
	// the tenant a route acts on are both on the request line. The membership,
	// SSO provider and domain routes are decided in that tenant's own policy
	// domain.
	pathMembers         = "/:tenant/members"
	pathMember          = pathMembers + "/:account"
	pathMemberExempt    = pathMember + "/sso-exempt"
	pathProviders       = "/:tenant/sso/providers"
	pathProvider        = "/:tenant/sso/providers/:id"
	pathProviderDomains = pathProvider + "/domains"
	pathProviderDomain  = pathProviderDomains + "/:domain"
	pathDomainVerify    = pathProviderDomain + "/verify"
	pathDomainReverify  = pathProvider + "/reverify-domains"
	pathMetadataRefresh = pathProvider + "/metadata/refresh"
)

// The inputs of the module's operations. Each carries the request's parameters
// and, where the operation takes one, the body fields the entry point binds.
// The descriptions are the contract's: the fragment publishes them.

// accountListInput is the account list's closed query set.
type accountListInput struct {
	Status            string `query:"status" description:"Filters by account status: disabled, active or deleted. Absent or empty selects every status except deleted; deleted selects the tombstones. Any other value is 400."`
	Kind              string `query:"kind" description:"Filters by account kind: user or service. Absent or empty selects both; any other value is 400."`
	IdentifierPrefix  string `query:"identifier_prefix" description:"Filters by the leading part of one identifier of the kind named in identifier_kind. The prefix is normalized the way stored identifiers are (a username lower-cased, an email address trimmed and lower-cased) and must be at least 3 characters. It must be given together with identifier_kind; one without the other is 400."`
	IdentifierKind    string `query:"identifier_kind" description:"The identifier kind the prefix applies to: username, email or phone. Required whenever identifier_prefix is given, and refused without it."`
	DisplayNamePrefix string `query:"display_name_prefix" description:"The account's display name by its leading bytes, case-sensitive; must be valid UTF-8 text; absent or empty selects every display name"`
	After             string `query:"after" description:"The next_cursor of the previous page, or empty for the first page. The cursor is the identifier of the last account on that page, and the page is read by it rather than by an offset."`
	Limit             int    `query:"limit" description:"The page size. Absent or 0 takes the default of 100; a value above 1000 is capped at 1000 rather than refused; a negative or non-integer value is 400."`
}

// accountCreateInput is the create's body: a user account names at least one
// login identifier; a service account is not held to that.
type accountCreateInput struct {
	_                struct{}          `description:"The account to create: a user account or a service account, with the login identifiers an administrator states."`
	Kind             string            `json:"kind" required:"true" enum:"user,service" description:"user for a human account, service for a non-interactive API client."`
	DisplayName      string            `json:"display_name" description:"The account's display name; absent writes an empty one."`
	Username         string            `json:"username" description:"A login name to add. At least one of username, email and phone is required for a user account; a username is lower-cased before it is stored."`
	Email            string            `json:"email" description:"An email address to add, asserted by the administrator. It is trimmed and lower-cased before it is stored, and it needs email_verified_via."`
	EmailVerifiedVia string            `json:"email_verified_via" enum:",admin,import-unverified" description:"How the administrator states the email was established: admin or import-unverified. It is required whenever email is present. The value records the administrator's assertion, not proof of ownership: an account with no proved email address is gated until the address is proved; empty or absent is refused with 400."`
	Phone            string            `json:"phone" description:"A phone number to add, in E.164 with a leading plus sign and 7 to 15 digits; it needs phone_verified_via."`
	PhoneVerifiedVia string            `json:"phone_verified_via" enum:",admin,import-unverified" description:"How the administrator states the phone was established: admin or import-unverified. It is required whenever phone is present. The value records the administrator's assertion, not proof of ownership; empty or absent is refused with 400."`
	Password         string            `json:"password" description:"An initial password; absent leaves the account without one. It must pass the password policy."`
	Attributes       map[string]string `json:"attributes" description:"Absent writes no attribute set"`
	MFARequired      bool              `json:"mfa_required" description:"True when the account must register a factor before it can sign in."`
}

// accountIDInput is the input of a route that addresses one account.
type accountIDInput struct {
	ID string `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
}

// accountPatchInput is a partial update: each field left out is left as it is.
type accountPatchInput struct {
	_           struct{}          `description:"A partial change to one account: a member left out is left as it is."`
	ID          string            `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	DisplayName *string           `json:"display_name" description:"Absent leaves the display name as it is"`
	Attributes  map[string]string `json:"attributes" description:"Absent leaves the attribute set as it is"`
	Protected   *bool             `json:"protected" description:"Absent leaves the protected flag as it is"`
}

// accountStatusInput changes an account's status.
type accountStatusInput struct {
	_      struct{} `description:"An account's new status."`
	ID     string   `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Status string   `json:"status" required:"true" enum:"active,disabled" description:"The deleted state is reached only through the delete route"`
}

// identifierAddInput adds one identifier to an account.
type identifierAddInput struct {
	_           struct{} `description:"A login identifier to add to one account, with how the administrator states it was established."`
	ID          string   `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Kind        string   `json:"kind" required:"true" enum:"username,email" description:"A phone number is added or changed only through an SMS code and is refused here"`
	Value       string   `json:"value" required:"true" description:"The identifier's text; it is normalized before it is stored, so a username is lower-cased and an email address is trimmed and lower-cased."`
	VerifiedVia string   `json:"verified_via" enum:",admin" description:"How the administrator states the identifier was established: admin records an assertion, not proof of ownership"`
}

// identifierRemoveInput removes one identifier by the identifier row's own
// identifier.
type identifierRemoveInput struct {
	ID         string `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Identifier string `path:"identifier" description:"The identifier's own identifier, as the profile surface reports it; not the identifier's value."`
}

// passwordResetInput sets an administrator's temporary password.
type passwordResetInput struct {
	_        struct{} `description:"An administrator's temporary password for one account."`
	ID       string   `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Password string   `json:"password" required:"true" description:"The temporary password, handed out once and never echoed, logged or audited"`
}

// mfaFlagInput sets whether an account must register a factor.
type mfaFlagInput struct {
	_        struct{} `description:"Whether one account must register a factor before it can sign in."`
	ID       string   `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Required bool     `json:"required" required:"true" description:"True when the account must register a factor before it can sign in."`
}

// secretCreateInput mints a service secret for a service account.
type secretCreateInput struct {
	_         struct{}   `description:"A service secret to mint: its label and, optionally, its expiry."`
	ID        string     `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Label     string     `json:"label" description:"The label for the administrator's own reference: at most 100 bytes of valid UTF-8 with no control character."`
	ExpiresAt *time.Time `json:"expires_at" description:"Absent mints a secret with no expiry"`
}

// secretRevokeInput revokes a service secret by its identifier.
type secretRevokeInput struct {
	ID     string `path:"id" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Secret string `path:"secret" description:"The service secret's identifier, not the secret value."`
}

// memberListInput is a tenant's member page.
type memberListInput struct {
	Tenant string `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	Limit  int    `query:"limit" description:"The page size. Absent or 0 takes the default of 100; a value above 1000 is capped at 1000 rather than refused; a negative or non-integer value is 400."`
	After  string `query:"after" description:"The account identifier of the last membership on the previous page, or empty for the first page."`
}

// memberWriteInput writes one account's membership in one tenant.
type memberWriteInput struct {
	_         struct{} `description:"A membership write: whether the account is an active member of the tenant and whether the tenant's SSO requirement exempts it."`
	Tenant    string   `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	Account   string   `path:"account" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Active    bool     `json:"active" description:"True adds or resumes the membership; false suspends it, and a suspended membership reads as not a member."`
	Source    string   `json:"source" description:"Absent records the change as an administrator's"`
	SSOExempt bool     `json:"sso_exempt" description:"True exempts the member from the tenant's enforced SSO requirement."`
}

// memberAddressInput addresses one membership.
type memberAddressInput struct {
	Tenant  string `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	Account string `path:"account" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
}

// memberExemptInput sets a member's SSO exemption.
type memberExemptInput struct {
	_         struct{} `description:"A member's exemption from the tenant's enforced SSO requirement."`
	Tenant    string   `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	Account   string   `path:"account" description:"The account's identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	SSOExempt bool     `json:"sso_exempt" required:"true" description:"True exempts the member; false clears the exemption."`
}

// tenantInput addresses one tenant.
type tenantInput struct {
	Tenant string `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
}

// providerCreateInput creates a provider in a tenant. The path and the body must
// agree on the tenant.
type providerCreateInput struct {
	_                 struct{}                   `description:"An identity provider as an administrator creates or updates it: the protocol, the provisioning mode and the protocol's own configuration."`
	Tenant            string                     `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	BodyTenant        string                     `json:"tenant" description:"Absent on a create takes the path's tenant; a value that names another is refused"`
	Protocol          string                     `json:"protocol" required:"true" description:"The protocol: oidc or saml. The set is closed."`
	Name              string                     `json:"name" required:"true" description:"The provider's display name; 1 to 64 characters."`
	Enabled           *bool                      `json:"enabled" description:"Absent leaves the enabled flag as it is (false on a create)"`
	Provisioning      string                     `json:"provisioning" required:"true" description:"jit creates an account on first sign-in; preprovisioned matches only an account an administrator has already created. Required, with no default."`
	TrustIDPMFA       *bool                      `json:"trust_idp_mfa" description:"Absent leaves the flag as it is (false on a create)"`
	AllowIDPInitiated *bool                      `json:"allow_idp_initiated" description:"Absent leaves the flag as it is (false on a create)"`
	AllowSHA1         *bool                      `json:"allow_sha1" description:"Absent leaves the flag as it is (false on a create)"`
	MergeExisting     *bool                      `json:"merge_existing" description:"Absent leaves the flag as it is (false on a create)"`
	Config            map[string]json.RawMessage `json:"config" description:"The protocol's own configuration"`
	ClientSecret      *string                    `json:"client_secret" description:"Absent writes no client secret on a create and keeps the stored one on an update"`
}

// providerUpdateInput replaces a provider. The tenant is immutable.
type providerUpdateInput struct {
	_                 struct{}                   `description:"An identity provider as an administrator creates or updates it: the protocol, the provisioning mode and the protocol's own configuration."`
	Tenant            string                     `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	ID                string                     `path:"id" description:"The provider's identifier."`
	BodyTenant        string                     `json:"tenant" description:"Absent on a create takes the path's tenant; a value that names another is refused"`
	Protocol          string                     `json:"protocol" required:"true" description:"The protocol: oidc or saml. The set is closed."`
	Name              string                     `json:"name" required:"true" description:"The provider's display name; 1 to 64 characters."`
	Enabled           *bool                      `json:"enabled" description:"Absent leaves the enabled flag as it is (false on a create)"`
	Provisioning      string                     `json:"provisioning" required:"true" description:"jit creates an account on first sign-in; preprovisioned matches only an account an administrator has already created. Required, with no default."`
	TrustIDPMFA       *bool                      `json:"trust_idp_mfa" description:"Absent leaves the flag as it is (false on a create)"`
	AllowIDPInitiated *bool                      `json:"allow_idp_initiated" description:"Absent leaves the flag as it is (false on a create)"`
	AllowSHA1         *bool                      `json:"allow_sha1" description:"Absent leaves the flag as it is (false on a create)"`
	MergeExisting     *bool                      `json:"merge_existing" description:"Absent leaves the flag as it is (false on a create)"`
	Config            map[string]json.RawMessage `json:"config" description:"The protocol's own configuration"`
	ClientSecret      *string                    `json:"client_secret" description:"Absent writes no client secret on a create and keeps the stored one on an update"`
}

// providerIDInput addresses one provider.
type providerIDInput struct {
	Tenant string `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	ID     string `path:"id" description:"The provider's identifier."`
}

// domainClaimInput claims a domain for a provider.
type domainClaimInput struct {
	_      struct{} `description:"A domain to claim for one identity provider."`
	Tenant string   `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	ID     string   `path:"id" description:"The provider's identifier."`
	Domain string   `json:"domain" required:"true" description:"The domain to claim, for example example.com; it is trimmed and lower-cased before it is stored."`
}

// domainAddressInput addresses one claimed domain of one provider.
type domainAddressInput struct {
	Tenant string `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	ID     string `path:"id" description:"The provider's identifier."`
	Domain string `path:"domain" description:"The claimed domain name."`
}

// sessionRevokeInput addresses one session by its identifier. The identifier is
// a session's, not an account's, so it does not carry the account shape's
// description.
type sessionRevokeInput struct {
	ID string `path:"id" description:"The session's identifier."`
}

// impersonationListInput is a tenant's impersonation-record page.
type impersonationListInput struct {
	Tenant string `path:"tenant" description:"The tenant whose records the page holds, as the tenant identifier; it scopes the read and fills the decision's tenant."`
	Limit  int    `query:"limit" description:"The page size. Absent or 0 takes the default of 100; a value above 1000 is capped at 1000 rather than refused; a negative or non-integer value is 400."`
	After  string `query:"after" description:"The next_cursor of the previous page, or empty for the first page; the cursor is the identifier of the last record on that page."`
}

// impersonationIDInput addresses one impersonation record.
type impersonationIDInput struct {
	Tenant string `path:"tenant" description:"The tenant's identifier (the tenant module's ID as a string), not an alias or a display name."`
	ID     string `path:"id" description:"The impersonation record's identifier."`
}

// The types the reply answers declare their JSON bodies with. The simple form's
// success body is its Out type, so it needs none of these.

var (
	accountEnvelopeType  = reflect.TypeOf(AuthnAdminAccountEnvelope{})
	factorListType       = reflect.TypeOf(AuthnAdminFactorList{})
	removedType          = reflect.TypeOf(AuthnAdminRemoved{})
	sessionListType      = reflect.TypeOf(AuthnAdminSessionList{})
	revokedType          = reflect.TypeOf(AuthnAdminRevoked{})
	secretListType       = reflect.TypeOf(AuthnAdminSecretList{})
	secretCreatedType    = reflect.TypeOf(AuthnAdminSecretCreated{})
	providerEnvelopeType = reflect.TypeOf(AuthnAdminProviderEnvelope{})
	verifyTokenType      = reflect.TypeOf(AuthnAdminVerifyToken{})
)

// challengeHeader declares the header every 401 on this module's routes carries:
// the gateway writes the endpoint's challenge when it refuses a request first,
// and a handler that answers 401 itself returns it in the fault's headers.
var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

// unauthorized is the declaration of a 401 that carries the capability's
// challenge.
func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

// commonFailures is the answer every route on this module's mounter shares: the
// gateway refuses a request that carries no usable credential before the
// handler runs.
func commonFailures() []speedhttp.Response {
	return []speedhttp.Response{unauthorized("No usable credential")}
}

// jsonResponse is a success answer carrying a JSON body of one shape.
func jsonResponse(status int, description string, structure reflect.Type) speedhttp.Response {
	return speedhttp.Response{
		Status:      status,
		Description: description,
		Body:        &speedhttp.Body{MediaType: "application/json", Structure: structure},
	}
}

// problemResponse is an error answer carrying one of this capability's problem
// codes. The type is what the entry point writes when it refuses the request
// itself with that status — a body it could not bind — so a request refused
// before the handler runs is answered in the same vocabulary as one the handler
// refuses.
func problemResponse(status int, code, description string) speedhttp.Response {
	return speedhttp.ProblemResponseOf(status, description, typePrefix+code)
}

// The account group.

// accountListOperation declares GET /admin/accounts, one page at a time.
var accountListOperation = speedhttp.NewOperation[accountListInput, AuthnAdminAccountList](
	"authn.admin.accounts.list",
	http.MethodGet,
	pathAccounts,
	"List accounts, one page at a time",
	"Returns one page of accounts in ascending identifier order, which for the UUIDv7 identifiers the module generates is close to creation order. Deleted accounts (tombstones) are left out unless the status filter asks for them. Every accepted filter given an empty value means no filter, and the query parameter set is closed: a parameter not declared here is refused with 400 rather than ignored. Requires the account.read action on the platform scope.",
	nil,
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The list's query could not be read, or a query parameter the route does not accept"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not read accounts"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusOK, Description: "The page of accounts and the next cursor", Body: &speedhttp.Body{MediaType: "application/json"}},
	),
)

// accountCreateOperation declares POST /admin/accounts.
var accountCreateOperation = speedhttp.NewOperation[accountCreateInput, AuthnAdminAccountEnvelope](
	"authn.admin.accounts.create",
	http.MethodPost,
	pathAccounts,
	"Create an account",
	"Creates a user or a service account. A user account must name at least one login identifier in this request — username, email or phone — because an account without one could not sign in; a user account that states a password additionally needs a username or email address, since a phone number is not a password login name and one without either is a 409 login-identifier-required. A service account signs in with a service secret and is not held to either rule. An email address or phone number is written with the way the administrator may state (admin or import-unverified), never with a proof of ownership. Creating a service account additionally needs the client.manage action. The mandatory audit row is written before the account row.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The account to create. kind is required; a user account also names at least one of username, email and phone, and email and phone each carry a verification channel in email_verified_via and phone_verified_via.",
		Required:    true,
		Name:        "AuthnAdminAccountCreate",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or an argument is invalid"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not create accounts"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusConflict, "An account with that identifier already exists (conflict), the email address is reserved by another account (email-taken), or a password account would have no username or email address to log in with (login-identifier-required)"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusCreated, Description: "The created account", Body: &speedhttp.Body{MediaType: "application/json"}},
	),
)

// accountGetOperation declares GET /admin/accounts/:id. It is a reply operation
// because a missing account is a bare 404, which the simple form cannot write.
var accountGetOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.get",
	http.MethodGet,
	pathAccount,
	"Read one account",
	"Reads one account by its identifier. A deleted account reads as not found, as does an identifier that never existed. Requires the account.read action, with the account as both the decision's target and target account.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not read the account"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The account", accountEnvelopeType),
	),
)

// accountPatchOperation declares PATCH /admin/accounts/:id.
var accountPatchOperation = speedhttp.NewReplyOperation[accountPatchInput](
	"authn.admin.accounts.patch",
	http.MethodPatch,
	pathAccount,
	"Update an account",
	"Writes the display name, the whole attribute set and the protected flag, each only when the request names it; a field left out is left as it is. protected is a host's mark that the account may not be impersonated, independent of any Decider. Requires account.update.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The fields to write. Every field is optional and each one left out leaves the stored value unchanged; attributes replaces the whole set rather than merging into it.",
		Required:    true,
		Name:        "AuthnAdminAccountPatch",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or an argument is invalid"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not update the account"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The updated account", accountEnvelopeType),
	),
)

// accountDeleteOperation declares DELETE /admin/accounts/:id.
var accountDeleteOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.delete",
	http.MethodDelete,
	pathAccount,
	"Delete an account",
	"Deletes the account and erases its personal data, leaving a tombstone that keeps the identifier, kind, status and creation time: identifiers, external bindings, password credential, service secrets and memberships are removed. Requires account.delete and a fresh local multi-factor authentication from the caller.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not delete the account"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The account was deleted"},
	),
)

// accountStatusOperation declares POST /admin/accounts/:id/status.
var accountStatusOperation = speedhttp.NewReplyOperation[accountStatusInput](
	"authn.admin.accounts.status",
	http.MethodPost,
	pathStatus,
	"Change an account's status",
	"Disables or restores an account; status is active or disabled. The deleted state is reached only through the delete route and is refused here. Requires account.disable and a fresh local multi-factor authentication. Sessions of a disabled account are cleaned up synchronously; a cleanup failure answers 503 while the status change stands, and retrying the same request completes the cleanup idempotently.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The status to set. Only active and disabled are accepted.",
		Required:    true,
		Name:        "AuthnAdminAccountStatus",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or names no known status"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not change the status"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The account at its new status", accountEnvelopeType),
	),
)

// identifierAddOperation declares POST /admin/accounts/:id/identifiers.
var identifierAddOperation = speedhttp.NewReplyOperation[identifierAddInput](
	"authn.admin.accounts.addIdentifier",
	http.MethodPost,
	pathIdentities,
	"Add an identifier to an account",
	"Adds a login name (username) or an email address to the account. The identifier is recorded as administrator-asserted (verified_via admin), which is not evidence of ownership, so it does not by itself justify automatic linking or merging. An identifier the account already holds keeps a proof it already has, so a proved address is not lowered to an assertion. A phone number is refused here: it is added or changed only through an SMS code. Requires account.update.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The identifier to add. kind is username or email and value is its text; the value is normalized before it is stored, so a username is lower-cased and an email address is trimmed and lower-cased.",
		Required:    true,
		Name:        "AuthnAdminIdentifierWrite",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or the identifier is malformed"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not add the identifier"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusConflict, "The identifier is bound elsewhere (conflict), or the email address is reserved by another account (email-taken)"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The identifier was added"},
	),
)

// identifierRemoveOperation declares DELETE
// /admin/accounts/:id/identifiers/:identifier.
var identifierRemoveOperation = speedhttp.NewReplyOperation[identifierRemoveInput](
	"authn.admin.accounts.removeIdentifier",
	http.MethodDelete,
	pathIdentity,
	"Remove an identifier from an account",
	"Removes the identifier named by its own identifier (the identifier row's id, not its value) from the account. Removing an identifier the account does not hold succeeds without changing anything. A password account cannot lose its last username or email address: that removal is a 409 login-identifier-required and changes nothing. A removal that would leave the account with no usable way to sign in beside a passkey is a 409 last-login-method and changes nothing. Requires account.update.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not remove the identifier"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account; an identifier the account does not hold is already unbound and answers 204"},
		problemResponse(http.StatusConflict, mount.CodeLastLoginMethod,
			"The removal would leave a password account with no login name (login-identifier-required), or leave the account with no way to sign in other than a passkey (last-login-method)"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The identifier was removed"},
	),
)

// passwordResetOperation declares POST /admin/accounts/:id/password.
var passwordResetOperation = speedhttp.NewReplyOperation[passwordResetInput](
	"authn.admin.accounts.resetPassword",
	http.MethodPost,
	pathPassword,
	"Reset an account's password",
	"Sets a temporary password that the account must change at its next sign-in, then revokes every session of the account. The revocation runs after the password write, so its failure is logged and does not undo the reset. The password is never echoed, logged or audited. An account with no username or email address has nothing to log in with, so setting a password is a 409 login-identifier-required. Requires account.update, a fresh local non-SMS multi-factor authentication and a seasoned factor of the caller's own; a caller without one gets 403 factor-not-seasoned.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The temporary password. It is written with must_change set, so the account must choose a new one at its next sign-in.",
		Required:    true,
		Name:        "AuthnAdminPasswordReset",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or the password fails the policy"),
		unauthorized("No usable credential, or a step-up is required"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating, may not reset the password, or has no seasoned non-SMS factor (factor-not-seasoned)"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		problemResponse(http.StatusConflict, mount.CodeLoginIdentifierRequired,
			"The account has no username or email address to log in with"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The password was set"},
	),
)

// factorListOperation declares GET /admin/accounts/:id/factors.
var factorListOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.factors",
	http.MethodGet,
	pathFactors,
	"List an account's registered factors",
	"Lists the account's registered factors as kind, label and status, and nothing of their credentials. Requires account.read.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not read the factors"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The account's factors", factorListType),
	),
)

// mfaFlagOperation declares POST /admin/accounts/:id/mfa-required.
var mfaFlagOperation = speedhttp.NewReplyOperation[mfaFlagInput](
	"authn.admin.accounts.setMFARequired",
	http.MethodPost,
	pathMFAFlag,
	"Set whether an account must register a factor",
	"Sets the account-level flag that requires the account to register a factor. Requires account.update.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The flag to write; an absent required writes false.",
		Required:    true,
		Name:        "AuthnAdminMFARequired",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not change the flag"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The flag was written"},
	),
)

// mfaResetOperation declares POST /admin/accounts/:id/mfa-reset.
var mfaResetOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.resetMFA",
	http.MethodPost,
	pathMFAReset,
	"Remove an account's registered factors",
	"Disables every factor registered on the account, sets the account's mfa_required flag so it must register a factor again, revokes every session of the account, and answers how many factors were removed. Requires mfa.reset, a fresh local non-SMS multi-factor authentication and a seasoned non-SMS factor of the caller's own; a caller without one gets 403 factor-not-seasoned. An account whose only way to log in is a factor is refused 409 last-login-method, because resetting it would leave the account with no way in; it must first be given a password, a phone number whose ownership was proved, or an external identity. A phone number an administrator stated does not count.",
	nil,
	speedhttp.Failures(commonFailures(),
		unauthorized("No usable credential, or a step-up is required"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating, may not reset the factors, or has no seasoned non-SMS factor (factor-not-seasoned)"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusConflict, "The account has no way to log in other than a factor, so resetting would lock it out (last-login-method)"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The number of factors removed", removedType),
	),
)

// The session group.

// accountSessionsOperation declares GET /admin/accounts/:id/sessions.
var accountSessionsOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.sessions",
	http.MethodGet,
	pathAccountSessions,
	"List an account's sessions",
	"Lists the account's live browser and token sessions, most recently created first, without any credential or token material. Each row's current is true when it is the caller's own session. Requires session.read.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not read the sessions"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The account's sessions", sessionListType),
	),
)

// revokeSessionsOperation declares POST /admin/accounts/:id/revoke-sessions.
var revokeSessionsOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.revokeSessions",
	http.MethodPost,
	pathRevokeSessions,
	"Revoke every session of an account",
	"Revokes every unrevoked browser and token session of the account, including one whose deadline has already passed, and answers with the number revoked. One audit row per session is written before the revocation, so the set read and the set revoked are the same; an account with nothing in that set revokes nothing and writes nothing. Requires session.revoke. Retrying is idempotent.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not revoke the sessions"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The number of sessions revoked", revokedType),
	),
)

// sessionRevokeOperation declares DELETE /admin/sessions/:id.
var sessionRevokeOperation = speedhttp.NewReplyOperation[sessionRevokeInput](
	"authn.admin.sessions.revoke",
	http.MethodDelete,
	pathSession,
	"Revoke one session by its identifier",
	"Revokes one session by its identifier. The session is read first to learn its account, which fills the decision's target account. Requires session.revoke. Revoking an already revoked session succeeds with the same answer.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not revoke the session"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such session"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The session was revoked"},
	),
)

// The service-secret group.

// secretListOperation declares GET /admin/accounts/:id/secrets.
var secretListOperation = speedhttp.NewReplyOperation[accountIDInput](
	"authn.admin.accounts.secrets.list",
	http.MethodGet,
	pathSecrets,
	"List an account's service secrets",
	"Lists the account's service secrets oldest first, as identifier, label, absolute expiry and revocation state; the secret value itself is never returned. Requires account.read.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not read the secrets"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The account's service secrets", secretListType),
	),
)

// secretCreateOperation declares POST /admin/accounts/:id/secrets.
var secretCreateOperation = speedhttp.NewReplyOperation[secretCreateInput](
	"authn.admin.accounts.secrets.create",
	http.MethodPost,
	pathSecrets,
	"Create a service secret for an account",
	"Mints a service secret for a service account; only a service account has them, so any other account is 400. The secret value is returned in this response only and can never be read again. An expiry, when given, must be in the future. Requires client.manage and a fresh local multi-factor authentication.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The secret to mint. label may be absent or empty, in which case the secret carries an empty label; an absent expires_at mints a secret with no expiry.",
		Required:    true,
		Name:        "AuthnAdminSecretCreate",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not create the secret"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusCreated, "The secret's identifier, label, one-time secret and expiry", secretCreatedType),
	),
)

// secretRevokeOperation declares DELETE /admin/accounts/:id/secrets/:secret.
var secretRevokeOperation = speedhttp.NewReplyOperation[secretRevokeInput](
	"authn.admin.accounts.secrets.revoke",
	http.MethodDelete,
	pathSecret,
	"Revoke a service secret",
	"Revokes the service secret named by its identifier, not by the secret value. Requires client.manage and a fresh local multi-factor authentication.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not revoke the secret"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account or secret"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The secret was revoked"},
	),
)

// The membership group.

// memberListOperation declares GET /admin/tenants/:tenant/members.
var memberListOperation = speedhttp.NewOperation[memberListInput, AuthnAdminMemberList](
	"authn.admin.members.list",
	http.MethodGet,
	pathMembers,
	"List a tenant's members",
	"Lists one tenant's memberships, one page at a time, in ascending account-identifier order. The path names the tenant identifier itself, not an alias: the module holds no tenant roster and does not look the tenant up, and the same identifier is the decision's tenant. Requires membership.manage.",
	nil,
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The page query could not be read"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not read memberships"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusOK, Description: "The tenant's members", Body: &speedhttp.Body{MediaType: "application/json"}},
	),
)

// memberWriteOperation declares PUT /admin/tenants/:tenant/members/:account.
var memberWriteOperation = speedhttp.NewReplyOperation[memberWriteInput](
	"authn.admin.members.put",
	http.MethodPut,
	pathMember,
	"Write an account's membership in a tenant",
	"Writes one account's membership in one tenant: active true adds or resumes it and active false suspends it, and a suspended membership reads as no membership. source records how it came to exist; omitted records it as an administrator's change. The account must exist and not be a tombstone. Requires membership.manage with the path's tenant.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The membership to write. active, sso_exempt and source are all optional on the wire, but an absent active writes false, which suspends the membership.",
		Required:    true,
		Name:        "AuthnAdminMemberWrite",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, an unknown field included, the tenant is invalid, or source is longer than 128 bytes. The module has no role."),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not write memberships"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The membership was written"},
	),
)

// memberRemoveOperation declares DELETE /admin/tenants/:tenant/members/:account.
// It is a reply operation because the capability's failures are answered as a
// bare 404 and as 400/409 problem documents, which the simple form cannot
// write.
var memberRemoveOperation = speedhttp.NewReplyOperation[memberAddressInput](
	"authn.admin.members.remove",
	http.MethodDelete,
	pathMember,
	"Remove an account's membership from a tenant",
	"Removes the account's membership from the tenant. Requires membership.manage with the path's tenant. The route is idempotent: a membership that is not there is already removed, and the call answers 204.",
	nil,
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The tenant or account identifier is invalid"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not remove memberships"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such account"},
		problemResponse(http.StatusConflict, mount.CodeConflict, "The membership cannot be removed in its current state"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The membership was removed"},
	),
)

// memberExemptOperation declares PUT
// /admin/tenants/:tenant/members/:account/sso-exempt.
var memberExemptOperation = speedhttp.NewReplyOperation[memberExemptInput](
	"authn.admin.members.ssoExempt",
	http.MethodPut,
	pathMemberExempt,
	"Set an account's exemption from a tenant's SSO requirement",
	"Sets or clears a member's emergency exemption from the tenant's enforced SSO requirement. The membership must already exist: an absent one is 404, and the route changes only the exemption, leaving the rest of the membership as it is. Requires membership.manage.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The exemption flag to write; false clears the exemption.",
		Required:    true,
		Name:        "AuthnAdminMemberSSOExempt",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not write the exemption"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such membership"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The exemption was written"},
	),
)

// The SSO provider group.

// providerListOperation declares GET /admin/tenants/:tenant/sso/providers.
var providerListOperation = speedhttp.NewOperation[tenantInput, AuthnAdminProviderList](
	"authn.admin.sso.providers.list",
	http.MethodGet,
	pathProviders,
	"List a tenant's SSO providers",
	"Lists the tenant's SSO providers. The encrypted client secret never appears. Requires idp.manage with the path's tenant.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not read providers"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusOK, Description: "The tenant's providers", Body: &speedhttp.Body{MediaType: "application/json"}},
	),
)

// providerCreateOperation declares POST /admin/tenants/:tenant/sso/providers.
var providerCreateOperation = speedhttp.NewReplyOperation[providerCreateInput](
	"authn.admin.sso.providers.create",
	http.MethodPost,
	pathProviders,
	"Create an SSO provider in a tenant",
	"Creates an SSO provider in the tenant. The path and the body must agree on the tenant: an absent body tenant takes the path's, and a different one is 400. protocol is oidc or saml; provisioning is jit or preprovisioned and has no default. A client secret is carried only by an OIDC provider. Every write raises the provider's version. Requires idp.manage.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The provider to create. protocol, name, provisioning and config are required; the config is validated against the protocol and an unknown key is refused.",
		Required:    true,
		Name:        "AuthnAdminProviderWrite",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or an argument is invalid"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not create providers"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusCreated, "The created provider", providerEnvelopeType),
	),
)

// providerGetOperation declares GET /admin/sso/providers/:id.
var providerGetOperation = speedhttp.NewReplyOperation[providerIDInput](
	"authn.admin.sso.providers.get",
	http.MethodGet,
	pathProvider,
	"Read one SSO provider",
	"Reads one provider by its identifier. The provider's own tenant, resolved from the store, is the decision's tenant. The encrypted client secret never appears. Requires idp.manage.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not read the provider"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The provider", providerEnvelopeType),
	),
)

// providerUpdateOperation declares PUT /admin/sso/providers/:id.
var providerUpdateOperation = speedhttp.NewReplyOperation[providerUpdateInput](
	"authn.admin.sso.providers.update",
	http.MethodPut,
	pathProvider,
	"Update an SSO provider",
	"A full write of the provider: protocol, name, provisioning and config must be present, and the config is validated with unknown keys refused. Booleans left out keep their stored value, and an omitted client_secret keeps the stored one. The tenant is immutable: a body naming another tenant is 400. Requires idp.manage; each write raises version.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The provider to write. protocol, name, provisioning and config are required; booleans left out keep their stored value, and an absent client_secret keeps the stored one.",
		Required:    true,
		Name:        "AuthnAdminProviderWrite",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or an argument is invalid"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not update the provider"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The updated provider", providerEnvelopeType),
	),
)

// providerDeleteOperation declares DELETE /admin/sso/providers/:id.
var providerDeleteOperation = speedhttp.NewReplyOperation[providerIDInput](
	"authn.admin.sso.providers.delete",
	http.MethodDelete,
	pathProvider,
	"Delete an SSO provider",
	"Deletes the provider. Requires idp.manage, with the provider's own tenant as the decision's tenant.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not delete the provider"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The provider was deleted"},
	),
)

// domainClaimOperation declares POST /admin/sso/providers/:id/domains.
var domainClaimOperation = speedhttp.NewReplyOperation[domainClaimInput](
	"authn.admin.sso.providers.claimDomain",
	http.MethodPost,
	pathProviderDomains,
	"Claim a domain for an SSO provider",
	"Claims a domain for the provider and answers the verification token to publish in the domain's DNS TXT record; the claim stays unverified until the verify route finds that record. Requires idp.manage. A domain another provider has already verified is 409; an unverified claim by another provider may stand beside this one.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The domain to claim, for example example.com. It is trimmed and lower-cased before it is stored.",
		Required:    true,
		Name:        "AuthnAdminDomainClaim",
	},
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read, or the domain field is empty. A non-empty domain that is not well formed is refused by the domain normalizer, whose failure this route does not map and answers 503."),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not claim domains"),
		problemResponse(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType, "The request body is not application/json"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider"},
		speedhttp.ProblemResponse(http.StatusConflict, "Another provider has already verified the domain; an unverified claim by another provider may stand beside this one"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The verification token to publish", verifyTokenType),
	),
)

// domainRemoveOperation declares DELETE
// /admin/sso/providers/:id/domains/:domain.
var domainRemoveOperation = speedhttp.NewReplyOperation[domainAddressInput](
	"authn.admin.sso.providers.removeDomain",
	http.MethodDelete,
	pathProviderDomain,
	"Remove a domain from an SSO provider",
	"Removes a claimed domain, verified or not, from the provider. Requires idp.manage.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not write domains"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider; removing a claim the provider does not hold succeeds without changing anything and answers 204"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The domain was removed"},
	),
)

// domainVerifyOperation declares POST
// /admin/sso/providers/:id/domains/:domain/verify.
var domainVerifyOperation = speedhttp.NewReplyOperation[domainAddressInput](
	"authn.admin.sso.providers.verifyDomain",
	http.MethodPost,
	pathDomainVerify,
	"Verify a claimed domain of an SSO provider",
	"Verifies a claimed domain by looking up the TXT record that must hold the verification token and, when it does, marks the claim verified. A domain the provider holds no claim on, or a record that does not hold the token, marks nothing verified. Requires idp.manage.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not write domains"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider; a domain with no claim and a TXT record without the token are not mapped by this route and answer 503"},
		speedhttp.ProblemResponse(http.StatusConflict, "Another provider has verified the domain, so the uniqueness index refuses this verification"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The domain was verified"},
	),
)

// reverifyDomainsOperation declares POST
// /admin/sso/providers/:id/reverify-domains.
var reverifyDomainsOperation = speedhttp.NewReplyOperation[providerIDInput](
	"authn.admin.sso.providers.reverifyDomains",
	http.MethodPost,
	pathDomainReverify,
	"Re-verify every claimed domain of an SSO provider",
	"Re-runs verification for every domain the provider has verified; a claim that was never verified is not looked up. Requires idp.manage.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not write domains"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The re-verification ran"},
	),
)

// metadataRefreshOperation declares POST
// /admin/sso/providers/:id/metadata/refresh.
var metadataRefreshOperation = speedhttp.NewReplyOperation[providerIDInput](
	"authn.admin.sso.providers.refreshMetadata",
	http.MethodPost,
	pathMetadataRefresh,
	"Refresh an SSO provider's metadata",
	"Refreshes the provider's metadata by dispatching to the refresher registered for its protocol. A protocol with no refresher in this assembly is 400, and a provider with no metadata URL cannot be refreshed. On a parse or validation failure the stored config is kept and the failure is answered. Requires idp.manage; a successful refresh writes the new config and raises version.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusBadRequest, "The provider's protocol has no registered refresher in this assembly, or its stored config cannot be decoded. The request carries no body."),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller may not refresh metadata"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such provider"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		jsonResponse(http.StatusOK, "The refreshed provider", providerEnvelopeType),
	),
)

// The impersonation-record group.

// impersonationListOperation declares GET /tenants/:tenant/impersonations.
var impersonationListOperation = speedhttp.NewOperation[impersonationListInput, AuthnAdminImpersonationList](
	"authn.admin.impersonations.list",
	http.MethodGet,
	pathImpersonations,
	"List impersonation records, one page at a time",
	"Lists the impersonation records of the tenant named by the path, one page at a time, in ascending record-identifier order. The tenant scopes the read and fills the decision's tenant, so the page holds only that tenant's records and the cursor counts them. Records are never deleted, and a record still in force whose deadline has passed is evaluated and stored as ended before it is returned. Requires impersonation.read.",
	nil,
	speedhttp.Failures(commonFailures(),
		problemResponse(http.StatusBadRequest, mount.CodeBadRequest, "The page query could not be read"),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not read records"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusOK, Description: "The page of records and the next cursor", Body: &speedhttp.Body{MediaType: "application/json"}},
	),
)

// impersonationRevokeOperation declares DELETE /tenants/:tenant/impersonations/:id.
var impersonationRevokeOperation = speedhttp.NewReplyOperation[impersonationIDInput](
	"authn.admin.impersonations.revoke",
	http.MethodDelete,
	pathImpersonation,
	"Revoke an impersonation record",
	"Revokes an impersonation record; the record is read first, including an already ended one, to learn its tenant and target, which fill the decision. Revoking a record that has already ended is a no-op and gets the same answer as the first revocation. This route writes no mandatory audit: revoking is a move in the safe direction, and the impersonation module writes its own best-effort row. Requires impersonation.revoke.",
	nil,
	speedhttp.Failures(commonFailures(),
		speedhttp.ProblemResponse(http.StatusForbidden, "The caller is impersonating or may not revoke records"),
		speedhttp.Response{Status: http.StatusNotFound, Description: "No such record"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{Status: http.StatusNoContent, Description: "The record was revoked"},
	),
)

// systemOperations is the part of the API decided in the platform domain:
// accounts, their sessions and their service secrets, and the impersonation
// records of a tenant (the tenant is the data the records are confined to).
var systemOperations = speedhttp.Operations{
	accountListOperation,
	accountCreateOperation,
	accountGetOperation,
	accountPatchOperation,
	accountDeleteOperation,
	accountStatusOperation,
	identifierAddOperation,
	identifierRemoveOperation,
	passwordResetOperation,
	factorListOperation,
	mfaFlagOperation,
	mfaResetOperation,
	accountSessionsOperation,
	revokeSessionsOperation,
	sessionRevokeOperation,
	secretListOperation,
	secretCreateOperation,
	secretRevokeOperation,
	impersonationListOperation,
	impersonationRevokeOperation,
}

// tenantOperations is the part of the API decided in the tenant's own policy
// domain: memberships and identity providers.
var tenantOperations = speedhttp.Operations{
	memberListOperation,
	memberWriteOperation,
	memberRemoveOperation,
	memberExemptOperation,
	providerListOperation,
	providerCreateOperation,
	providerGetOperation,
	providerUpdateOperation,
	providerDeleteOperation,
	domainClaimOperation,
	domainRemoveOperation,
	domainVerifyOperation,
	reverifyDomainsOperation,
	metadataRefreshOperation,
}

// operations is every operation the module serves, for the checks that hold the
// declaration as a whole. The fragments are generated from the two scoped
// slices, not from this one.
var operations = append(append(speedhttp.Operations{}, systemOperations...), tenantOperations...)
