package main

// The self-service organization-creation host module: a verified account opens
// a business organization of its own.
//
// The route is POST /api/v1/orgs on the account endpoint. That endpoint resolves
// no tenant and is where a caller who has not chosen one yet stands, so the
// creation is decided by the caller's own account and nothing else. The tenant
// itself is opened through tenant.Provisioner, the same seam the platform
// management surface uses: the creator becomes the tenant-admin, the role is
// marked builtin and carries the governed permission set, and the membership's
// source is the administrator's.
//
// The upper bounds are the product's: a verified email address, five active
// business organizations per account, three creations an hour per account, a
// thirty-second creation lock so one account's concurrent requests do not race
// each other, and a mandatory audit row. The two numeric limits are runtime
// settings (org.create.max-organizations and org.create.max-per-hour); the
// switch and the bounds' shape are configuration.
//
// The route coexists with POST /api/v1/orgs/personal. Both are exact routes on
// the same account endpoint, whose /api/v1/orgs claim is configuration and
// stays claimed either way: with this module's switch off the path reaches the
// endpoint's own 404 rather than being taken over by the personal prefix.

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// orgCreateModuleName is the name this host module is registered under, and the
// namespace its configuration and its runtime settings hang from.
const orgCreateModuleName = "org.create"

// orgCreateFeature is the feature this host module declares. It is conditional:
// the module is named by the baseline's serve command, and the feature is in
// force only when the configuration switch is on and the route is mounted.
const orgCreateFeature = orgCreateModuleName

// orgCreateConfigItem is the switch, relative to the module's namespace.
const orgCreateConfigItem = "enabled"

// orgCreatePathFragment is the route as the fragment writes it: relative to the
// API root the module's Spec declares.
const orgCreatePathFragment = "/orgs"

// orgCreateEndpoint is the listening endpoint the route is mounted on and the
// endpoint the OpenAPI fragment describes. It is fixed: the route is deliberately
// on the endpoint that resolves no tenant.
const orgCreateEndpoint = "account"

// orgCreateIDPrefix is the fixed prefix of every generated organization
// identifier and alias. The rest is a random UUID, so the string carries no
// display name or other personal data and cannot be predicted from one.
const orgCreateIDPrefix = "org-"

// orgCreateIdentifierRetries is how many times a generated identifier is
// retried after the first when the roster already holds it. The pre-check can
// only see this replica's snapshot, so a cross-replica collision is still
// possible and is answered by the provisioner's own refusal.
const orgCreateIdentifierRetries = 3

// orgCreateLockTTL is how long one account's creation lock lives when the
// process that took it stops before releasing it.
const orgCreateLockTTL = 30 * time.Second

// The runtime-setting items the module declares. The keys are relative to the
// module's namespace.
const (
	orgCreateMaxOrganizationsItem = "max-organizations"
	orgCreateMaxPerHourItem       = "max-per-hour"
)

// The full paths of the two runtime settings, as the settings store names them.
const (
	orgCreateMaxOrganizationsPath = orgCreateModuleName + "." + orgCreateMaxOrganizationsItem
	orgCreateMaxPerHourPath       = orgCreateModuleName + "." + orgCreateMaxPerHourItem
)

// The problem types the module writes, in the org namespace the personal
// tenant's refusals also use. A code the frontend renders by code has to keep
// the urn:speed:<module>:<dashed-code> shape, because @speed/api-client
// normalizes each dash to an underscore when it names the i18n key.
const (
	orgCreateProblemPrefix         = "urn:speed:org:"
	orgCreationInProgressCode      = "creation-in-progress"
	orgOrganizationLimitCode       = "organization-limit"
	orgRateLimitedCode             = "rate-limited"
	orgIdentifierUnavailableCode   = "identifier-unavailable"
	orgCreateAuditAction           = "org.tenant.create"
	orgCreateOutcomeSuccess        = "success"
	orgCreateOutcomeFailed         = "failed"
	orgCreateDetailActionID        = "action_id"
	orgCreateDetailAccount         = "account"
	orgCreateDetailTenant          = "tenant"
	orgCreateFragmentTitle         = "organization create"
	orgCreateFragmentVersion       = "1.0.0"
	orgCreateMaxOrganizationsStart = 5
	orgCreateMaxPerHourStart       = 3
)

// errOrgIdentifierUnavailable reports a generated identifier or alias the
// roster already holds after every retry. Retrying does not succeed.
var errOrgIdentifierUnavailable = errors.New("the generated organization identifier or alias is unavailable")

//go:embed org-create-openapi.json
var orgCreateFragment []byte

// orgCreateSpec is the API root the module's fragment and route hang off. The
// route hangs off the account endpoint's /api/v1/orgs claim; the prefix is
// written into every configuration, so it stays claimed even when the switch is
// off (the claim is configuration, the mounting is assembly: an off switch
// answers the path with the endpoint's own 404).
var orgCreateSpec = speedhttp.Spec{
	Endpoint: orgCreateEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: orgCreateFragment,
}

// orgCreateRoutePath is the absolute path the creation is mounted on: the
// fragment path resolved against the API root.
var orgCreateRoutePath = orgCreateSpec.ResolvePath(orgCreatePathFragment)

// createOrganizationInput is the request body of the self-service creation. The
// caller gives a display name and a description; the identifier and the alias
// are generated, so no technical name reaches a person filling the form.
type createOrganizationInput struct {
	_ struct{} `description:"The display name and description of the organization to create. The identifier and alias are generated."`

	DisplayName string `json:"display_name" required:"true" description:"The organization's display name; 1 to 100 runes after trimming, and the surface requires it non-empty."`
	Description string `json:"description" description:"The organization's description; at most 500 runes after trimming."`
}

// OrganizationCreatedReply is the creation's success answer.
type OrganizationCreatedReply struct {
	_ struct{} `description:"The organization this call created, with the generated identifier and alias."`

	Tenant OrganizationCreatedTenant `json:"tenant" required:"true"`
}

// OrganizationCreatedTenant is the created organization as the answer renders
// it. The identifier and the alias are the same generated string.
type OrganizationCreatedTenant struct {
	_ struct{} `description:"The created organization: the generated identifier and alias (the same string), the profile the caller gave, and the active state."`

	ID          string `json:"id" required:"true" description:"The generated organization identifier: the fixed prefix \"org-\" followed by a random UUID. The scoped tables hold it."`
	Alias       string `json:"alias" required:"true" description:"The request-surface name, equal to the identifier for a self-service organization."`
	DisplayName string `json:"displayName" required:"true" description:"The organization's display name, trimmed."`
	Description string `json:"description" required:"true" description:"The organization's description, trimmed."`
	Status      string `json:"status" required:"true" enum:"active" description:"The organization's state; a self-service organization starts active."`
}

// orgCreateRateLimitedResponse is the 429 answer with the Retry-After header
// declared. A handler's Fault supplies the value; the declaration publishes the
// header on the contract.
func orgCreateRateLimitedResponse() speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusTooManyRequests,
		"The account has reached the hourly creation limit; Retry-After names the seconds until the next window")
	response.Headers = []speedhttp.Header{{
		Name:        "Retry-After",
		Description: "The number of seconds until the hourly window opens again",
		Value:       "1",
	}}
	return response
}

// orgCreateCreateOperation declares POST /orgs: the creation of one business
// organization for the calling account.
var orgCreateCreateOperation = speedhttp.NewOperation[createOrganizationInput, OrganizationCreatedReply](
	"org.create.create",
	http.MethodPost,
	orgCreatePathFragment,
	"Create a business organization",
	"Creates one active business organization for the calling account through the provisioning seam: the "+
		"caller becomes its first tenant-admin, the tenant-admin role is marked builtin and carries the governed "+
		"permission set. The identifier and alias are generated together as one org-<uuid> string. A pending "+
		"email address is refused (contact-required); an impersonating caller, a service account and a static "+
		"subject are refused. A per-account lock, the active-business-organization cap and the hourly creation "+
		"limit bound the call, and a mandatory audit row must land before the provisioner runs.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The display name and description of the organization; the identifier and alias are generated.",
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
			"The display name is missing or the profile is outside its bounds"),
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is impersonating, is a service account or a static subject, or has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The caller cannot create an organization now: a creation is already in progress "+
				"(creation-in-progress), the account holds the maximum number of active business "+
				"organizations (organization-limit), or the generated identifier is unavailable "+
				"(identifier-unavailable). The type field names which"),
		orgCreateRateLimitedResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"A dependency could not answer; no part of the organization is created"),
	},
)

// orgCreateOperations is this module's declaration of the API it serves. The
// fragment is generated from it; the route is mounted from the same declaration.
var orgCreateOperations = speedhttp.Operations{orgCreateCreateOperation}

// init registers this module. The import is the wiring; the serve command's
// Include list is what puts it in a run.
func init() {
	core.ProcessRegistry.Register(orgCreateModule())
}

// orgCreateModule is the descriptor of this host's self-service creation.
//
// The roster, the memberships, the signup-verification read, the provisioner,
// the key-value store, the settings store and the audit capability are
// required: every decision the route makes reads one of them, and an assembly
// that names this module without one cannot answer correctly. The HTTP entry
// point, the feature board, the gateway's registration surface and the logger
// are optional.
func orgCreateModule() core.Module {
	return core.Module{
		Name: orgCreateModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*tenant.Provisioner)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*feature.Board)(nil), Optional: true},
			{Token: (*authn.Gate)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			orgCreateConfigSchema(),
			orgCreateSettingsSchema(),
			orgCreateSpec,
			orgCreateOperations,
			feature.Spec{Name: orgCreateFeature, Conditional: true},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := readOrgCreateConfig(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if !cfg.Enabled {
				return core.Enablement{
					State:  core.StateDisabled,
					Reason: orgCreateModuleName + ": " + orgCreateConfigItem + " is off",
				}, nil
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newOrgCreateProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := orgCreateProductOf(instance)
			if !ok {
				return nil
			}
			return product.wire(reg)
		},
	}
}

// orgCreateConfig is the module's configuration.
type orgCreateConfig struct {
	// Enabled is the switch. Off, the route is not mounted and the feature is
	// not in force; the claimed /api/v1/orgs prefix then answers the account
	// endpoint's own 404.
	Enabled bool `config:"enabled"`
}

// orgCreateConfigSchema declares the module's switch. The defaults document
// turns it on for a bare run; a deployment that turns it off mounts nothing.
func orgCreateConfigSchema() config.Schema {
	return config.Schema{
		Namespace: orgCreateModuleName,
		Mounts:    []config.Mount{{Value: &orgCreateConfig{}}},
		Items: map[string]config.Item{
			orgCreateConfigItem: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "whether an account may create a business organization of its own; off, " +
					"POST /api/v1/orgs is not mounted and the feature org.create is absent",
			},
		},
	}
}

// orgCreateLimitDefaults is the carrier of the two runtime settings. Its field
// values are the declared defaults.
type orgCreateLimitDefaults struct {
	MaxOrganizations int
	MaxPerHour       int
}

// orgCreateSettingsSchema declares the two runtime settings. Both are global
// items: the tenant settings surface hides every org.create key through the
// tenant-admin module's platform-only roots, so a tenant administrator cannot
// read or write them.
func orgCreateSettingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: orgCreateModuleName,
		Mounts: []settings.Mount{{Value: &orgCreateLimitDefaults{
			MaxOrganizations: orgCreateMaxOrganizationsStart,
			MaxPerHour:       orgCreateMaxPerHourStart,
		}}},
		Items: map[string]settings.Item{
			orgCreateMaxOrganizationsItem: {
				Description: "how many active business organizations one account may hold before a " +
					"creation is refused; a personal tenant and a disabled tenant are not counted",
				Domain: settings.DomainOf("the positive integers", func(limit int) bool { return limit > 0 }),
			},
			orgCreateMaxPerHourItem: {
				Description: "how many organizations one account may create in one hour, a count " +
					"consumed by every request that passes all preliminary checks and reaches the " +
					"provisioner, whether the provisioner succeeds or not",
				Domain: settings.DomainOf("the positive integers", func(limit int) bool { return limit > 0 }),
			},
		},
	}
}

// readOrgCreateConfig decodes the switch. Prepare and New both go through it so
// the two see one configuration.
func readOrgCreateConfig(reg *core.Registry) (orgCreateConfig, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return orgCreateConfig{}, fmt.Errorf("%s: taking up the configuration reader: %w", orgCreateModuleName, err)
	}
	var cfg orgCreateConfig
	if err := reader.Decode(orgCreateModuleName, &cfg); err != nil {
		return orgCreateConfig{}, err
	}
	return cfg, nil
}

// orgCreateProduct is the module's instance: the seams each decision reads.
type orgCreateProduct struct {
	config       orgCreateConfig
	roster       tenant.Catalog
	memberships  authn.Memberships
	verification authn.SignupVerification
	settings     settings.Store
	kv           kv.KV
	provisioner  tenant.Provisioner
	audit        audit.Audit
	logger       *slog.Logger
	// now is the clock the hourly window and Retry-After are read from. It is a
	// field so a case can move the window without sleeping.
	now func() time.Time
	// newIdentifier mints one candidate identifier. It is a field for the same
	// reason: a case can force a collision the random generator would not.
	newIdentifier func() string
}

// orgCreateProductOf narrows an instance to this module's product.
func orgCreateProductOf(instance any) (*orgCreateProduct, bool) {
	product, ok := instance.(*orgCreateProduct)
	return product, ok && product != nil
}

// newOrgCreateProduct takes up what the descriptor requires.
func newOrgCreateProduct(reg *core.Registry) (*orgCreateProduct, error) {
	cfg, err := readOrgCreateConfig(reg)
	if err != nil {
		return nil, err
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", orgCreateModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the memberships: %w", orgCreateModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the signup verification read: %w", orgCreateModuleName, err)
	}
	provisioner, err := core.Resolve[tenant.Provisioner](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant provisioner: %w", orgCreateModuleName, err)
	}
	counters, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the key-value store: %w", orgCreateModuleName, err)
	}
	store, err := core.Resolve[settings.Store](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the settings store: %w", orgCreateModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", orgCreateModuleName, err)
	}
	product := &orgCreateProduct{
		config:        cfg,
		roster:        roster,
		memberships:   memberships,
		verification:  verification,
		settings:      store,
		kv:            counters,
		provisioner:   provisioner,
		audit:         auditor,
		logger:        log.Default(),
		now:           time.Now,
		newIdentifier: newOrgCreateIdentifier,
	}
	if delivered, found, takeErr := optionalOrgCreate[log.Logger](reg, "the logger"); takeErr != nil {
		return nil, takeErr
	} else if found {
		product.logger = delivered.Named(orgCreateModuleName)
	}
	return product, nil
}

// optionalOrgCreate takes up a capability whose absence is a legal assembly.
func optionalOrgCreate[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	value, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return value, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", orgCreateModuleName, what, err)
	}
}

// wire mounts the route and posts the feature when the switch is on.
func (p *orgCreateProduct) wire(reg *core.Registry) error {
	if !p.config.Enabled {
		p.logger.Info(orgCreateModuleName + ": the self-service creation is off; the route is not mounted")
		return nil
	}
	router, found, err := optionalOrgCreate[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		p.logger.Info(orgCreateModuleName + ": the self-service creation is on but the run has no HTTP entry point; nothing is mounted")
		return nil
	}
	endpoint, err := router.Endpoint(orgCreateEndpoint)
	if err != nil {
		return fmt.Errorf("%s: %w: %q: %w", orgCreateModuleName, speedhttp.ErrUnknownEndpoint, orgCreateEndpoint, err)
	}
	endpoint.Route(orgCreateCreateOperation.Method, orgCreateRoutePath,
		orgCreateCreateOperation.Handle(p.handleCreate))
	// The route is registered for an impersonating identity so the handler can
	// refuse it with a 403 rather than let the gateway answer its own 401. The
	// refusal is the product's: an impersonating operator does not open an
	// organization in the impersonated account's name.
	if gate, gateFound, gateErr := optionalOrgCreate[authn.Gate](reg, "the authentication gateway"); gateErr != nil {
		return gateErr
	} else if gateFound {
		gate.Impersonable(orgCreateEndpoint, http.MethodPost, orgCreateRoutePath)
	}
	if board, boardFound, boardErr := optionalOrgCreate[feature.Board](reg, "the feature board"); boardErr != nil {
		return boardErr
	} else if boardFound {
		board.Post(orgCreateModuleName, orgCreateFeature)
	}
	p.logger.Info(orgCreateModuleName+": the self-service creation is on",
		"endpoint", orgCreateEndpoint, "route", orgCreateRoutePath)
	return nil
}

// handleCreate serves POST /api/v1/orgs. The order of the decisions is the
// product's and each step reads only what it needs:
//
//  1. an identity is required;
//  2. an impersonating identity is refused before anything is read;
//  3. a service account and a static subject are refused;
//  4. the account's email address must not be pending (a read failure is 503);
//  5. the profile is normalized and held to its shape;
//  6. the per-account creation lock is taken;
//  7. the active-business-organization cap is checked;
//  8. the hourly count is checked;
//  9. one attempt is counted;
//  10. the mandatory audit row lands;
//  11. the tenant is provisioned.
//
// A refusal in steps 1-8 and 10 writes no count. The count is taken before the
// audit so an audit failure does not consume an attempt; a provisioner failure
// does consume it, as the product states.
func (p *orgCreateProduct) handleCreate(ctx context.Context, in createOrganizationInput) (OrganizationCreatedReply, error) {
	identity, ok := authn.FromContext(ctx)
	if !ok {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	if identity.Impersonating() {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	if slices.Contains(identity.Methods(), authn.MethodClientSecret) {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	account := identity.Account()
	if account == "" || account.IsStatic() {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}

	status, err := p.verification.SignupStateOf(ctx, account)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		// No account row holds no email address; the rest of the pipeline answers
		// for a caller that should not have an identity at all.
	case err != nil:
		p.logger.Error(orgCreateModuleName+": the signup state could not be read, so the creation was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	case status.State == authn.SignupPending:
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusForbidden,
			Type:   authn.ContactRequiredProblemType,
		}
	}

	profile := tenant.Profile{
		DisplayName: in.DisplayName,
		Description: in.Description,
	}.Normalize()
	if profile.DisplayName == "" || profile.Validate() != nil {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusBadRequest}
	}

	lockKey := orgCreateLockKey(account)
	locked, err := p.kv.SetIfAbsent(ctx, lockKey, []byte(p.now().UTC().Format(time.RFC3339Nano)), orgCreateLockTTL)
	if err != nil {
		p.logger.Error(orgCreateModuleName+": the creation lock could not be taken",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if !locked {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   orgCreateProblemPrefix + orgCreationInProgressCode,
		}
	}
	defer p.releaseLock(lockKey)

	limits, err := p.limits()
	if err != nil {
		p.logger.Error(orgCreateModuleName+": the creation limits could not be read, so the creation was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	active, err := p.activeBusinessOrganizations(ctx, account)
	if err != nil {
		p.logger.Error(orgCreateModuleName+": the account's business organizations could not be counted",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if active >= limits.MaxOrganizations {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   orgCreateProblemPrefix + orgOrganizationLimitCode,
		}
	}

	count, err := p.hourlyCount(ctx, account)
	if err != nil {
		p.logger.Error(orgCreateModuleName+": the hourly creation count could not be read",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if count >= limits.MaxPerHour {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status:  http.StatusTooManyRequests,
			Type:    orgCreateProblemPrefix + orgRateLimitedCode,
			Headers: []speedhttp.HeaderValue{{Name: "Retry-After", Value: strconv.Itoa(p.retryAfter())}},
		}
	}

	record, err := p.newOrganization(ctx, profile)
	if err != nil {
		if errors.Is(err, errOrgIdentifierUnavailable) {
			return OrganizationCreatedReply{}, speedhttp.Fault{
				Status: http.StatusConflict,
				Type:   orgCreateProblemPrefix + orgIdentifierUnavailableCode,
			}
		}
		p.logger.Error(orgCreateModuleName+": a free identifier could not be chosen",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	if err := p.recordAttempt(ctx, account); err != nil {
		p.logger.Error(orgCreateModuleName+": the creation could not be counted, so it was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	actionID := uuid.NewString()
	if err := p.auditRequired(ctx, audit.Entry{
		Action:  orgCreateAuditAction,
		Object:  string(record.ID),
		Outcome: orgCreateOutcomeSuccess,
		Detail: map[string]string{
			orgCreateDetailActionID: actionID,
			orgCreateDetailAccount:  account.String(),
			orgCreateDetailTenant:   string(record.ID),
		},
	}); err != nil {
		return OrganizationCreatedReply{}, err
	}

	if err := p.provisioner.Provision(ctx, record, account.String()); err != nil {
		if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrAliasConflict) {
			// Another replica won the identifier between this call's pre-check
			// and its provisioning. The domain belongs to the winner, and the
			// mandatory row this request wrote already records the attempt, so
			// no second row is written for the same request.
			p.logger.Warn(orgCreateModuleName+": the generated identifier was taken by another replica",
				"account", account.String(), "tenant", string(record.ID), "cause", err.Error())
			return OrganizationCreatedReply{}, speedhttp.Fault{
				Status: http.StatusConflict,
				Type:   orgCreateProblemPrefix + orgIdentifierUnavailableCode,
			}
		}
		p.auditBestEffort(ctx, audit.Entry{
			Action:  orgCreateAuditAction,
			Object:  string(record.ID),
			Outcome: orgCreateOutcomeFailed,
			Detail:  map[string]string{orgCreateDetailActionID: actionID},
		})
		p.logger.Error(orgCreateModuleName+": the organization could not be provisioned",
			"account", account.String(), "tenant", string(record.ID), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	return OrganizationCreatedReply{
		Tenant: OrganizationCreatedTenant{
			ID:          string(record.ID),
			Alias:       record.Alias,
			DisplayName: record.Profile.DisplayName,
			Description: record.Profile.Description,
			Status:      record.Status.String(),
		},
	}, nil
}

// limits reads the two runtime settings from the global layer. A value outside
// the declaration's domain is refused rather than read as a limit: the
// declaration refuses such a write, but a row written around the store is
// served as it is, and a limit of zero would make every creation a refusal
// whose reason the caller cannot act on.
func (p *orgCreateProduct) limits() (orgCreateLimitDefaults, error) {
	var limits orgCreateLimitDefaults
	if err := p.settings.Get(settings.Global(), orgCreateMaxOrganizationsPath, &limits.MaxOrganizations); err != nil {
		return orgCreateLimitDefaults{}, fmt.Errorf("reading %s: %w", orgCreateMaxOrganizationsPath, err)
	}
	if err := p.settings.Get(settings.Global(), orgCreateMaxPerHourPath, &limits.MaxPerHour); err != nil {
		return orgCreateLimitDefaults{}, fmt.Errorf("reading %s: %w", orgCreateMaxPerHourPath, err)
	}
	if limits.MaxOrganizations <= 0 || limits.MaxPerHour <= 0 {
		return orgCreateLimitDefaults{}, fmt.Errorf("%s: a stored limit is not a positive integer", orgCreateModuleName)
	}
	return limits, nil
}

// activeBusinessOrganizations counts the organizations an account holds an
// active membership in that are active and not personal. The memberships are
// read first and the roster's rows in one call, so the count is two reads
// however many memberships the account holds.
func (p *orgCreateProduct) activeBusinessOrganizations(ctx context.Context, account authn.AccountID) (int, error) {
	held, err := p.memberships.Tenants(ctx, account)
	if err != nil {
		return 0, fmt.Errorf("reading the account's memberships: %w", err)
	}
	rows, err := p.roster.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading the roster: %w", err)
	}
	status := make(map[string]tenant.Status, len(rows))
	for _, row := range rows {
		status[string(row.ID)] = row.Status
	}
	count := 0
	for _, membership := range held {
		if !membership.Active || isPersonalTenantID(membership.Tenant) {
			continue
		}
		if status[membership.Tenant] == tenant.StatusActive {
			count++
		}
	}
	return count, nil
}

// hourlyCount reads the creation count of the account's current hourly window.
func (p *orgCreateProduct) hourlyCount(ctx context.Context, account authn.AccountID) (int, error) {
	raw, present, err := p.kv.Get(ctx, orgCreateHourlyKey(account, p.now()))
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("the hourly counter is not a number: %w", err)
	}
	return int(value), nil
}

// recordAttempt consumes one place in the account's hourly window. A missing
// key starts from zero with an expiry just past the next window boundary; a
// live key keeps its expiry, so the window is not extended by being counted.
func (p *orgCreateProduct) recordAttempt(ctx context.Context, account authn.AccountID) error {
	now := p.now()
	ttl := orgCreateNextHour(now).Sub(now) + time.Minute
	_, err := p.kv.IncrByFloatWithTTL(ctx, orgCreateHourlyKey(account, now), 1, ttl)
	return err
}

// retryAfter answers the whole seconds until the current window closes, at
// least one.
func (p *orgCreateProduct) retryAfter() int {
	now := p.now()
	seconds := int(math.Ceil(orgCreateNextHour(now).Sub(now).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// newOrganization generates one candidate identifier and pre-checks it against
// the roster, retrying the generation up to orgCreateIdentifierRetries times.
// The pre-check reads this replica's snapshot, so a row another replica wrote
// since the snapshot is invisible here; the provisioner's own refusal is the
// arbiter for that race.
func (p *orgCreateProduct) newOrganization(ctx context.Context, profile tenant.Profile) (tenant.Tenant, error) {
	for attempt := 0; attempt <= orgCreateIdentifierRetries; attempt++ {
		identifier := p.newIdentifier()
		if !tenant.IsIdentifier(identifier) {
			return tenant.Tenant{}, fmt.Errorf("%w: the generated identifier is outside the roster's domain",
				errOrgIdentifierUnavailable)
		}
		available, err := p.identifierAvailable(ctx, tenant.ID(identifier))
		if err != nil {
			return tenant.Tenant{}, err
		}
		if available {
			return tenant.Tenant{
				ID:      tenant.ID(identifier),
				Alias:   identifier,
				Status:  tenant.StatusActive,
				Profile: profile,
			}, nil
		}
	}
	return tenant.Tenant{}, errOrgIdentifierUnavailable
}

// identifierAvailable reports whether neither the identifier nor the alias (the
// same string) is held. Both lookups are made because the roster keeps the two
// as separate unique columns: a row whose identifier is another string may hold
// the alias.
func (p *orgCreateProduct) identifierAvailable(ctx context.Context, id tenant.ID) (bool, error) {
	if _, found, err := p.roster.Get(ctx, id); err != nil {
		return false, fmt.Errorf("reading the roster for %q: %w", id, err)
	} else if found {
		return false, nil
	}
	if _, found, err := p.roster.ByAlias(ctx, string(id)); err != nil {
		return false, fmt.Errorf("reading the roster's alias %q: %w", id, err)
	} else if found {
		return false, nil
	}
	return true, nil
}

// releaseLock drops the account's creation lock. The TTL is the backstop for a
// process that stops before this runs, so a failure here changes no answer and
// is only logged.
func (p *orgCreateProduct) releaseLock(key string) {
	if err := p.kv.Delete(context.Background(), key); err != nil {
		p.logger.Error(orgCreateModuleName+": the creation lock could not be released; its TTL will expire it",
			"key", key, "cause", err.Error())
	}
}

// auditRequired writes the mandatory row of a creation. A row that does not land
// is the audit module's state, and the creation is not carried out: the caller
// answers 503, with the cause in the log and never in the answer.
func (p *orgCreateProduct) auditRequired(ctx context.Context, entry audit.Entry) error {
	if err := p.audit.Record(ctx, entry); err != nil {
		p.logger.Error(orgCreateModuleName+": the mandatory audit row could not be written, so the organization was not created",
			"action", entry.Action, "tenant", entry.Object, "cause", err.Error())
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

// auditBestEffort writes the failed row that closes a failed attempt. Its own
// failure is a log line and changes nothing about the answer.
func (p *orgCreateProduct) auditBestEffort(ctx context.Context, entry audit.Entry) {
	if err := p.audit.Record(ctx, entry); err != nil {
		p.logger.Error(orgCreateModuleName+": a failed-action audit row could not be written",
			"action", entry.Action, "tenant", entry.Object, "cause", err.Error())
	}
}

// newOrgCreateIdentifier mints one candidate identifier: the fixed prefix and a
// random UUID. The alias is the same string, so the two never drift apart.
func newOrgCreateIdentifier() string {
	return orgCreateIDPrefix + uuid.NewString()
}

// orgCreateLockKey names the per-account creation lock.
func orgCreateLockKey(account authn.AccountID) string {
	return orgCreateModuleName + ".lock." + account.String()
}

// orgCreateHourlyKey names the account's count in the hourly window the instant
// belongs to. The window is the calendar hour in UTC, so every replica agrees
// on it without sharing a clock beyond the one the key arithmetic encodes.
func orgCreateHourlyKey(account authn.AccountID, now time.Time) string {
	return orgCreateModuleName + ".hourly." + account.String() + "." +
		strconv.FormatInt(now.UTC().Truncate(time.Hour).Unix(), 10)
}

// orgCreateNextHour is the instant the window now belongs to closes.
func orgCreateNextHour(now time.Time) time.Time {
	return now.UTC().Truncate(time.Hour).Add(time.Hour)
}
