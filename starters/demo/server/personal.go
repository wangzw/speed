package main

// The personal-tenant host module: the demo host's own policy that every
// account owns a personal tenant, so a registration completed through the
// sign-in surface can use the tenant area at once.
//
// The tenant is built by the account lifecycle's creation hook, after the
// account exists. The route is the backfill: a caller whose own personal tenant
// is missing from its membership list (an account whose creation hook failed)
// asks for one, and a caller that already holds it reads it back. The
// backfill's caller list is the client's own, and it is deliberately not
// qualified by the account's other memberships: an account in business tenants
// still owns its personal tenant.
//
// The decision is recorded in
// docs/adr/adr-starters-personal-tenant-2026-10-18.md and in the starter
// design's 个人租户 section. It is a host policy and not a capability: it sits
// above the tenant roster, the account store's memberships and the
// authorization layer, it is switched by this host's configuration (off by
// default, on in the review assembly), and no pkg/ package gains a surface for
// it.
//
// The route is POST /api/v1/workspace/personal on the account endpoint. That endpoint
// resolves no tenant, so the caller is served before any tenant context exists;
// the gateway guards the endpoint, and this module registers the route as one an
// impersonating identity may reach so the handler can answer it 403 itself
// (the gateway's refusal for an unregistered route is a 401).
//
// The tenant's identifier and alias are one deterministic string: a fixed
// prefix plus the account identifier. Nothing personal goes into it -- not the
// phone number, the display name or an email address -- because the alias
// travels in logs, audit rows and request paths. Two concurrent ensure calls
// derive the same string, so they collide on the roster's unique identifier and
// the loser reads the winner's row back.

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// personalModuleName is the name this host module is registered under, and the
// configuration namespace its single switch hangs from.
const personalModuleName = "workspace.personal"

// personalFeature is the feature this host module declares. It is conditional:
// the module is named by the baseline's serve command, and the feature is in
// force only when the configuration switch is on and the route is mounted.
const personalFeature = personalModuleName

// personalConfigItem is the switch, relative to the module's namespace.
const personalConfigItem = "enabled"

// personalPathFragment is the route as the fragment writes it: relative to the
// API root the module's Spec declares. The mounting resolves it to the absolute
// path the route is registered at.
const personalPathFragment = "/workspace/personal"

// personalEndpoint is the listening endpoint the route is mounted on and the
// endpoint the OpenAPI fragment describes. It is fixed: the route is deliberately
// on the one endpoint that resolves no tenant, so that ensure can run before the
// caller has a tenant to resolve.
const personalEndpoint = "account"

// personalIDPrefix is the fixed prefix of every personal tenant identifier and
// alias. The rest is the account identifier, so the string is deterministic and
// carries no personal data.
const personalIDPrefix = "personal-"

// personalTenantAdminRole is the role the ensure writes in the personal tenant.
// It is every tenant's own highest role, and the account that owns the personal
// tenant is its first and only holder.
const personalTenantAdminRole = authz.TenantAdminRole

// legacyPersonalOwnerRole is the role the old personal-tenant model wrote. The
// backfill removes it, with its grants and bindings, once the tenant-admin role
// and the account's binding are in place.
const legacyPersonalOwnerRole = authz.Role("owner")

// personalBusinessPermissions is the host-declared set of business-resource
// permissions a personal tenant's tenant-admin may hold. The set is written out
// in code and not derived from the process catalog, so a permission a module
// declares later does not join the role on its own. It is empty in this
// skeleton: the notification surface declares no permission point, and the
// object store no longer declares one — its terminal-user API is gone, and bytes
// are reached through a minted URL the business backend asks for. A product that
// adds a business capability adds its tenant-level permissions here.
var personalBusinessPermissions = []authz.Permission{}

// personalExcludedPermissions is the part of the business-resource face a
// personal tenant's tenant-admin must never hold. A personal tenant is a
// single-person space: membership management and invitations would let its
// holder add accounts, and identity-provider management would let it manage SSO.
// Both live in the authentication capability: invitations are decided on
// authn.membership.manage, and IdP management on authn.idp.manage.
var personalExcludedPermissions = []authz.Permission{
	authz.PermissionMembershipManage,
	authz.PermissionIDPManage,
}

// personalTenantAdminPermissions is the allowlist the ensure converges the
// tenant-admin role's grants to: the host-declared business resources less the
// management actions a personal tenant must not have.
func personalTenantAdminPermissions() []authz.Permission {
	return personalAllowlist(personalBusinessPermissions)
}

// personalAllowlist removes the excluded management permissions from a
// business-resource set.
func personalAllowlist(business []authz.Permission) []authz.Permission {
	out := make([]authz.Permission, 0, len(business))
	for _, permission := range business {
		if slices.Contains(personalExcludedPermissions, permission) {
			continue
		}
		out = append(out, permission)
	}
	return out
}

// personalTenantAdminDescriptionKey is the description key written with the
// tenant-admin role. It travels in the API answer wherever a role is listed; the
// demo renders no role surface of its own.
const personalTenantAdminDescriptionKey = tenantAdminDescriptionKey

// personalMemberSource is the membership source the ensure writes. The account
// module publishes it on the membership event; "personal" names this host's own
// provisioning rather than an administrator, an invitation or an import.
const personalMemberSource = "personal"

// personalKind is the answer's tenant kind. The frontend renders the personal
// workspace label from it; the tenant record itself carries no display name.
const personalKind = "personal"

// The problem types the module writes. A 403, a 401 and a 503 carry no type of
// their own in this surface; the 409s do, because the frontend distinguishes
// the refusals a retry cannot change from a dependency failure. Every code here
// is this surface's own, so the two surfaces do not share a code.
const (
	personalProblemPrefix     = "urn:speed:workspace:"
	tenantUnavailableCode     = "tenant-unavailable"
	identifierUnavailableCode = "identifier-unavailable"
)

// The audit vocabulary the module writes, one action per tenant-side write. An
// object is the personal tenant's identifier; the detail is a closed set: the
// action identifier links the rows of one attempt, and account and tenant name
// the two ends of the action. The shape is the management surface's.
const (
	personalCreateAction   = "workspace.personal.tenant.create"
	personalDeleteAction   = "workspace.personal.tenant.delete"
	personalOutcomeSuccess = "success"
	personalOutcomeFailed  = "failed"
	personalDetailActionID = "action_id"
	personalDetailAccount  = "account"
	personalDetailTenant   = "tenant"
)

// The ensure's own refusals. None is returned by a dependency and so none
// carries a cause.
var (
	// errTenantUnavailable refuses an account whose personal tenant row exists
	// and is not enabled. The ensure then writes nothing, so disabling the
	// tenant is how an operator withdraws a personal workspace.
	errTenantUnavailable = errors.New("the personal tenant exists and is not enabled")
	// errIdentifierUnavailable refuses an identifier or alias the personal
	// tenant cannot take: the derived identifier is outside the roster's
	// domain, or another tenant holds the alias. Retrying does not succeed.
	errIdentifierUnavailable = errors.New("the personal tenant's identifier or alias is unavailable")
	// errRosterLagging reports a row the create saw in the table and this
	// replica's roster snapshot does not carry yet. It is the expected race
	// between concurrent ensure calls and between replicas, and it is
	// retryable.
	errRosterLagging = errors.New("the roster's snapshot has not caught up with the tenant row")
)

// personalFragmentTitle and personalFragmentVersion are the info section the
// fragment personalOperations generates. The generation test compares the
// committed bytes with what these name.
const (
	personalFragmentTitle   = "workspace personal"
	personalFragmentVersion = "1.0.0"
)

//go:embed personal-openapi.json
var personalFragment []byte

// personalSpec is the API root the module's fragment and route hang off. The
// route hangs off the account endpoint's /api/v1/workspace claim; the prefix is
// written into every configuration and into the frontend's backend-prefix
// manifest, so it stays claimed even when the switch is off (the claim is
// configuration, the mounting is assembly: an off switch answers the prefix
// with 404).
var personalSpec = speedhttp.Spec{
	Endpoint: personalEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: personalFragment,
}

// personalRoutePath is the absolute path the ensure is mounted on: the fragment
// path resolved against the API root.
var personalRoutePath = personalSpec.ResolvePath(personalPathFragment)

// personalEnsureOperation declares the ensure route and binds its types: the
// handler receives no input and returns the reply, so the entry point binds the
// request and writes the declared success answer. The failure answers are the
// route's closed set; a refusal the declaration does not carry is answered 500.
var personalEnsureOperation = speedhttp.NewOperation[struct{}, WorkspacePersonalReply](
	"workspace.personal.ensure",
	http.MethodPost,
	personalPathFragment,
	"Ensure the caller's personal tenant exists and is complete",
	"Backfills the caller's personal tenant when the account's own membership is missing, and reads it "+
		"back otherwise. Every call completes the tenant's tenant-admin role and the caller's binding and "+
		"converges the role's grants to the host's allowlist. A membership row that exists is left as it "+
		"stands, and a tenant that is not enabled is refused without a write. The tenant identifier and "+
		"alias are derived from the account identifier; the answer names the tenant and whether this "+
		"call created it.",
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
			"The caller is impersonating, is a service account, or is a static-table subject that "+
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

// personalOperations is this module's declaration of the API it serves. The
// fragment is generated from it; the route is mounted from the same declaration.
var personalOperations = speedhttp.Operations{personalEnsureOperation}

// init registers this module. The import is the wiring, as for every host
// module; the serve command's Include list is what puts it in a run.
func init() { core.ProcessRegistry.Register(personalModule()) }

// personalModule is the descriptor of this host's personal-tenant policy.
//
// The roster, the memberships, the policy manager, the seeding surface, the
// permission catalog and the audit module are required: the ensure writes all
// five pieces, the deletion writes the audit row, and an assembly that names
// this module without one of them cannot run it. The HTTP entry point, the
// feature board, the route-declaration surface and the logger are optional:
// without an entry point the routes are not mounted, without a board the
// conditional feature is simply absent from the document, and without the
// gateway's registration surface the route is guarded by the gateway's default
// and an impersonating caller is answered by the gateway instead of the handler.
//
// The module provides the account lifecycle hook: the personal tenant follows
// the account that owns it, and the account module calls the hooks after its own
// transaction commits. Where the assembly also carries an event bus, the module
// subscribes the account events and runs the same idempotent hooks again, so a
// process that stopped between the account's commit and the hooks leaves nothing
// for good: the staging outbox delivers the event after the restart.
//
// The descriptor declares its OpenAPI fragment on the account endpoint: the
// contract belongs to the application's own second-level generation, not to a
// pkg/ package.
func personalModule() core.Module {
	return core.Module{
		Name: personalModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.MembershipCleanup)(nil)},
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Seeder)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*feature.Board)(nil), Optional: true},
			{Token: (*authn.Gate)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*event.Bus)(nil), Optional: true},
			{Token: (*authn.Accounts)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.AccountLifecycle)(nil)},
		},
		Resources: []any{
			personalSchema(),
			personalSpec,
			personalOperations,
			// The switch varies it, so it is posted in Init when the route is
			// mounted and absent otherwise.
			feature.Spec{Name: personalFeature, Conditional: true},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			if _, err := readPersonalConfig(reg); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newPersonalProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := personalProductOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},
	}
}

// personalConfig is the module's configuration.
type personalConfig struct {
	// Enabled is the switch. Off, the route is not mounted and the feature is
	// not in force; the claimed /api/v1/workspace prefix then answers 404.
	Enabled bool `config:"enabled"`
}

// personalSchema declares the module's one input item. The list is collected
// over every registered module whether or not it runs, so the namespace is the
// module's own name.
func personalSchema() config.Schema {
	return config.Schema{
		Namespace: personalModuleName,
		Mounts:    []config.Mount{{Value: &personalConfig{}}},
		Items: map[string]config.Item{
			personalConfigItem: {
				Origins: config.OriginPrimary,
				Description: "whether the host creates a personal tenant for an account with no " +
					"membership; off, POST /api/v1/workspace/personal is not mounted and /api/v1/workspace answers 404",
			},
		},
	}
}

// readPersonalConfig decodes the switch. Prepare and New both go through it so
// the two see one configuration.
func readPersonalConfig(reg *core.Registry) (personalConfig, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return personalConfig{}, fmt.Errorf("%s: taking up the configuration reader: %w", personalModuleName, err)
	}
	var cfg personalConfig
	if err := reader.Decode(personalModuleName, &cfg); err != nil {
		return personalConfig{}, err
	}
	return cfg, nil
}

// personalProduct is the module's instance: the seams the ensure and the
// lifecycle hooks write through, the audit seam their actions are recorded on,
// and the switch it runs on. It provides authn.AccountLifecycle, so the account
// module's status change and deletion reach the personal tenant through it.
type personalProduct struct {
	config      personalConfig
	roster      tenant.Catalog
	memberships authn.Memberships
	// membershipCleanup is the account-deletion removal path, which does not ask
	// the membership guard: the host's own guard refuses every removal in a
	// personal tenant, and the deletion's cleanup still has to collect the rows
	// an administrator added.
	membershipCleanup authn.MembershipCleanup
	manager           authz.Manager
	seeder      authz.Seeder
	catalog     authz.Catalog
	audit       audit.Audit
	logger      *slog.Logger
	// accounts is the read the event compensation makes to decide the account's
	// current state; it is nil in an assembly without the account read surface,
	// where the event's own target is the best evidence available.
	accounts personalAccountReader
	// bus is where the compensation subscribes; nil in an assembly without one,
	// which leaves the synchronous hooks as the only route.
	bus  event.Bus
	seen *personalSeenEvents
}

// personalProductOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied, and the callbacks leave it alone.
func personalProductOf(instance any) (*personalProduct, bool) {
	p, ok := instance.(*personalProduct)
	return p, ok && p != nil
}

// newPersonalProduct takes up what the descriptor requires.
func newPersonalProduct(reg *core.Registry) (*personalProduct, error) {
	cfg, err := readPersonalConfig(reg)
	if err != nil {
		return nil, err
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", personalModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the memberships: %w", personalModuleName, err)
	}
	membershipCleanup, err := core.Resolve[authn.MembershipCleanup](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account-deletion membership removal: %w", personalModuleName, err)
	}
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy manager: %w", personalModuleName, err)
	}
	seeder, err := core.Resolve[authz.Seeder](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy seeding surface: %w", personalModuleName, err)
	}
	catalog, err := core.Resolve[authz.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the permission catalog: %w", personalModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", personalModuleName, err)
	}
	p := &personalProduct{
		config:            cfg,
		roster:            roster,
		memberships:       memberships,
		membershipCleanup: membershipCleanup,
		manager:           manager,
		seeder:            seeder,
		catalog:           catalog,
		audit:             auditor,
		logger:            log.Default(),
		seen:              newPersonalSeenEvents(personalSeenEventsCapacity),
	}
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(personalModuleName)
	}
	if accounts, found, err := optionalPersonal[authn.Accounts](reg, "the account read surface"); err != nil {
		return nil, err
	} else if found {
		p.accounts = accounts
	}
	if bus, found, err := optionalPersonal[event.Bus](reg, "the event bus"); err != nil {
		return nil, err
	} else if found {
		p.bus = bus
	}
	return p, nil
}

// wire mounts the route and posts the feature when the switch is on. Every piece
// is idempotent-by-absence: an off switch or an assembly without an entry point
// mounts nothing, and the feature is posted only when the route is mounted.
//
// The event compensation is subscribed before the switch is read: the hooks are
// part of what the module provides to the account module whether or not the
// route is mounted, and a switch that is off withdraws the route, not the
// tenant's lifecycle. Subscribing it here rather than in New is what the bus
// requires: it accepts subscriptions in Init.
func (p *personalProduct) wire(reg *core.Registry) error {
	p.subscribe()
	if !p.config.Enabled {
		p.logger.Info(personalModuleName + ": the personal tenant is off; the route is not mounted")
		return nil
	}
	router, found, err := optionalPersonal[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		p.logger.Info(personalModuleName + ": the personal tenant is on but the run has no HTTP entry point; nothing is mounted")
		return nil
	}
	endpoint, err := router.Endpoint(personalEndpoint)
	if err != nil {
		return fmt.Errorf("%s: %w: %q: %w", personalModuleName, speedhttp.ErrUnknownEndpoint, personalEndpoint, err)
	}
	endpoint.Route(personalEnsureOperation.Method, personalRoutePath,
		personalEnsureOperation.Handle(p.handleEnsure))
	// An impersonating identity is let through the gateway so the handler can
	// answer it 403. Without the registration surface the gateway's default
	// refuses the route for an impersonating caller, which is a 401.
	if gate, found, gateErr := optionalPersonal[authn.Gate](reg, "the authentication gateway"); gateErr != nil {
		return gateErr
	} else if found {
		gate.Impersonable(personalEndpoint, http.MethodPost, personalRoutePath)
	}
	if board, found, boardErr := optionalPersonal[feature.Board](reg, "the feature board"); boardErr != nil {
		return boardErr
	} else if found {
		board.Post(personalModuleName, personalFeature)
	}
	p.logger.Info(personalModuleName+": the personal tenant is on",
		"endpoint", personalEndpoint, "route", personalRoutePath)
	return nil
}

// optionalPersonal takes up a capability whose absence is a legal assembly.
func optionalPersonal[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", personalModuleName, what, err)
	}
}

// WorkspacePersonalReply is the ensure's success answer. It is the operation's
// declared success body, so its exported name is the component name the
// fragment publishes.
type WorkspacePersonalReply struct {
	_ struct{} `description:"The answer to a successful ensure: the caller's personal tenant and whether this call registered it."`

	Tenant  WorkspacePersonalTenant `json:"tenant" required:"true"`
	Created bool                    `json:"created" required:"true" description:"Whether this call registered the tenant"`
}

// WorkspacePersonalTenant is the tenant the answer names. It carries no display
// name: the frontend renders the personal label from Kind.
type WorkspacePersonalTenant struct {
	_ struct{} `description:"The personal tenant the ensure names. It carries no display name: the frontend renders the personal label from the kind."`

	ID    string `json:"id" required:"true" description:"The tenant identifier, derived deterministically from the account identifier: the fixed prefix \"personal-\" followed by the account identifier, the same string the alias carries. The scoped tables hold it, so it takes no phone number, display name or other personal data, and it does not change with the request language"`
	Alias string `json:"alias" required:"true" description:"The request-surface name, equal to the identifier for a personal tenant"`
	Kind  string `json:"kind" required:"true" enum:"personal" description:"The tenant kind; the frontend renders the personal label from it"`
}

// personalResult is the ensure's outcome before it is rendered.
type personalResult struct {
	Tenant  tenant.Tenant
	Created bool
}

// handleEnsure serves POST /api/v1/workspace/personal.
//
// The caller has to be authenticated; the gateway has usually settled that, and
// the check here is what answers a run whose account endpoint is unguarded. An
// impersonating identity is a 403, a service account is a 403, a static-table
// subject is not an account and is a 403. An account whose personal tenant is not enabled is the 409
// tenant-unavailable, and an identifier or alias the tenant cannot take is the
// 409 identifier-unavailable. Every dependency failure is a 503 that carries no
// part of the result.
func (p *personalProduct) handleEnsure(ctx context.Context, _ struct{}) (WorkspacePersonalReply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return WorkspacePersonalReply{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	if id.Impersonating() {
		return WorkspacePersonalReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	if slices.Contains(id.Methods(), authn.MethodClientSecret) {
		// A service account authenticates only with its client secret, so the
		// method is the account kind: a non-interactive API client is given
		// tenant access by an administrator and gets no workspace of its own.
		return WorkspacePersonalReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	account := id.Account()
	if account == "" || account.IsStatic() {
		// A subject of the static credential table has no account row, so it
		// cannot hold a membership or own a tenant.
		return WorkspacePersonalReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	result, err := p.ensure(ctx, account)
	switch {
	case err == nil:
		return WorkspacePersonalReply{
			Tenant: WorkspacePersonalTenant{
				ID:    string(result.Tenant.ID),
				Alias: result.Tenant.Alias,
				Kind:  personalKind,
			},
			Created: result.Created,
		}, nil
	case errors.Is(err, errTenantUnavailable):
		p.logger.Warn(personalModuleName+": the personal tenant is not enabled",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		return WorkspacePersonalReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   personalProblemPrefix + tenantUnavailableCode,
		}
	case errors.Is(err, errIdentifierUnavailable):
		p.logger.Warn(personalModuleName+": the personal tenant's identifier is unavailable",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		return WorkspacePersonalReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   personalProblemPrefix + identifierUnavailableCode,
		}
	case errors.Is(err, errRosterLagging):
		// The expected race: another call or another replica wrote the row and
		// this replica's snapshot has not caught up. It is retryable, not a
		// failure of the run, so it is not an error-level record.
		p.logger.Warn(personalModuleName+": the roster's snapshot lags the personal tenant row",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		return WorkspacePersonalReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	default:
		p.logger.Error(personalModuleName+": the personal tenant could not be ensured",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		return WorkspacePersonalReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}

// personalAudit builds the mandatory row of a tenant-side write: the tenant's
// identifier is the object, and the detail names the action's two ends and the
// identifier that links the rows of one attempt.
func personalAudit(action string, account authn.AccountID, id tenant.ID, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  string(id),
		Outcome: personalOutcomeSuccess,
		Detail: map[string]string{
			personalDetailActionID: actionID,
			personalDetailAccount:  account.String(),
			personalDetailTenant:   string(id),
		},
	}
}

// personalFailedAudit builds the row that closes a failed attempt, linked to its
// permitted row by the action identifier.
func personalFailedAudit(action string, id tenant.ID, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  string(id),
		Outcome: personalOutcomeFailed,
		Detail:  map[string]string{personalDetailActionID: actionID},
	}
}

// newPersonalActionID mints the identifier that links the audit rows of one
// attempt. It is made after the decision and before the mandatory row, so the
// permitted row and any failed row beside it carry the same value.
func newPersonalActionID() string { return uuid.NewString() }

// auditRequired writes the mandatory row of an action. A row that does not land
// is the audit module's state, and the action is not carried out: the caller
// answers 503, with the cause in the log and never in the answer.
func (p *personalProduct) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(personalModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			"action", e.Action, "tenant", e.Object, "cause", err.Error())
		return fmt.Errorf("writing the %s audit row: %w", e.Action, err)
	}
	return nil
}

// auditBestEffort writes the failed row that closes a failed attempt. Its own
// failure is a log line and changes nothing about the answer.
func (p *personalProduct) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(personalModuleName+": a failed-action audit row could not be written",
			"action", e.Action, "tenant", e.Object, "cause", err.Error())
	}
}

// ensure checks and completes the five pieces of a personal tenant:
//
//  1. the tenant row (created, or read back when it already exists),
//  2. the account's membership in it,
//  3. the non-builtin tenant-admin role,
//  4. the tenant-admin role's grants, converged to the host's allowlist: the
//     missing ones added, the ones outside it revoked,
//  5. the account's binding to that role.
//
// Each step is idempotent, and a failure leaves the earlier steps in place for
// the next call to complete. What the ensure does not do is rewrite a decision
// an operator made: a membership row that exists is left as it stands, whatever
// its state, and a tenant row that is not enabled is answered with a refusal
// before anything is written.
//
// The eligibility check runs first: an account that already holds a membership
// in its personal tenant reads that tenant back. A membership elsewhere is not
// an eligibility fact — every account owns its personal tenant — so the
// backfill creates one even for an account that belongs to business tenants. An
// unproved email address is not an eligibility fact either: the workspace exists
// for the account, and the verification gate limits what the account may do
// inside it.
func (p *personalProduct) ensure(ctx context.Context, account authn.AccountID) (personalResult, error) {
	personalID := personalTenantID(account)
	// The roster admits an identifier without blanks or control characters and
	// with no length bound; the account identifier's own domain is 1 to 64
	// bytes of [A-Za-z0-9._-], so the derived string normally fits. A caller
	// outside that domain is refused rather than retried.
	if !tenant.IsIdentifier(string(personalID)) {
		return personalResult{}, fmt.Errorf("%w: the derived personal tenant identifier is outside "+
			"the roster's domain", errIdentifierUnavailable)
	}

	_, own, err := p.memberships.Membership(ctx, string(personalID), account)
	if err != nil {
		return personalResult{}, fmt.Errorf("reading the account's personal membership: %w", err)
	}

	var record tenant.Tenant
	created := false
	actionID := ""
	createBegan := false
	createRefused := false
	if own {
		record, err = p.registeredTenant(ctx, personalID)
	} else {
		// The mandatory row lands before the create: an audit module that cannot
		// take it stops the registration, so no tenant is ever created without the
		// record of it.
		actionID = newPersonalActionID()
		createBegan = true
		if auditErr := p.auditRequired(ctx, personalAudit(personalCreateAction, account, personalID, actionID)); auditErr != nil {
			return personalResult{}, auditErr
		}
		record, created, createRefused, err = p.createTenant(ctx, personalID)
	}
	if err != nil {
		if createBegan {
			p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
		}
		return personalResult{}, err
	}
	// fail closes whatever is left of a create attempt with the failed row that
	// links to its permitted one. A read-back attempt has no permitted row to
	// close.
	fail := func(err error) (personalResult, error) {
		if createBegan {
			p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
		}
		return personalResult{}, err
	}
	if record.Status != tenant.StatusActive {
		// A disabled tenant is the operator's decision. Nothing below runs, so
		// the next ensure leaves the row and its policy as they are.
		return fail(errTenantUnavailable)
	}

	if !own {
		if err = p.memberships.Put(ctx, authn.Membership{
			Account: account,
			Tenant:  string(personalID),
			Active:  true,
			Source:  personalMemberSource,
		}); err != nil {
			return fail(fmt.Errorf("writing the membership: %w", err))
		}
	}

	roles, err := p.manager.Roles(ctx, string(personalID))
	if err != nil {
		return fail(fmt.Errorf("reading the personal tenant's roles: %w", err))
	}
	adminPresent := false
	for _, role := range roles {
		if role.Role == personalTenantAdminRole {
			adminPresent = true
			break
		}
	}
	if !adminPresent {
		if err = p.manager.PutRole(ctx, authz.RoleRecord{
			Tenant:         string(personalID),
			Role:           personalTenantAdminRole,
			DescriptionKey: personalTenantAdminDescriptionKey,
			CreatedAt:      time.Now().UTC(),
		}); err != nil {
			return fail(fmt.Errorf("writing the tenant-admin role: %w", err))
		}
	}

	if err = p.convergeTenantAdminGrants(ctx, string(personalID)); err != nil {
		return fail(err)
	}

	bindings, err := p.manager.BindingsOf(ctx, string(personalID), account.String())
	if err != nil {
		return fail(fmt.Errorf("reading the account's bindings: %w", err))
	}
	bound := false
	for _, binding := range bindings {
		if binding.Role == personalTenantAdminRole {
			bound = true
			break
		}
	}
	if !bound {
		if err := p.manager.PutBinding(ctx, authz.Binding{
			Tenant:    string(personalID),
			Account:   account.String(),
			Role:      personalTenantAdminRole,
			GrantedAt: time.Now().UTC(),
		}); err != nil {
			return fail(fmt.Errorf("binding the account to the tenant-admin role: %w", err))
		}
	}

	// A tenant built under the old model still carries its owner role. The
	// tenant-admin role and the account's binding are in place above, so removing
	// the old role — with its grants and bindings — finishes the migration. An
	// absent role is no work, and a role an operator marked builtin is tolerated:
	// it cannot be deleted, and the tenant-admin role is already in place.
	if err := p.manager.DeleteRole(ctx, string(personalID), legacyPersonalOwnerRole); err != nil &&
		!errors.Is(err, authz.ErrBuiltinRole) {
		return fail(fmt.Errorf("removing the personal tenant's legacy owner role: %w", err))
	}

	if createRefused {
		// Another call won the race the deterministic identifier creates: this
		// attempt's create was refused, and the row it read back is the answer.
		p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
	}
	return personalResult{Tenant: record, Created: created}, nil
}

// registeredTenant reads the personal tenant's row back. The roster's Get reads
// this replica's snapshot, which may predate another replica's registration or
// this process's own commit, so EnterRegistered catches the snapshot up first:
// it admits from the snapshot as it stands, reads the table only where the
// snapshot would refuse, and brings a snapshot that is behind the table up to
// it.
func (p *personalProduct) registeredTenant(ctx context.Context, id tenant.ID) (tenant.Tenant, error) {
	if _, enterErr := p.roster.EnterRegistered(ctx, id); enterErr != nil {
		return tenant.Tenant{}, fmt.Errorf("catching the roster's snapshot up with the personal "+
			"tenant: %w", enterErr)
	}
	held, found, err := p.roster.Get(ctx, id)
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("reading the personal tenant back: %w", err)
	}
	if !found {
		return tenant.Tenant{}, fmt.Errorf("%w: identifier %q", errRosterLagging, id)
	}
	return held, nil
}

// createTenant registers the personal tenant. A row the roster's table already
// holds is read back instead: the deterministic identifier makes that the
// expected race between concurrent ensure calls, and the row is the answer.
// An alias another tenant holds, or a write that fails, is returned as it
// stands.
//
// The third result reports whether the roster refused the create call itself.
// A refusal the read-back recovers from still closes the attempt's audit row
// with a failed one, because the create this call tried did not happen.
func (p *personalProduct) createTenant(ctx context.Context, id tenant.ID) (tenant.Tenant, bool, bool, error) {
	record := tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	err := p.roster.Create(ctx, record)
	switch {
	case err == nil:
		return record, true, false, nil
	case errors.Is(err, tenant.ErrTenantExists):
		held, readErr := p.registeredTenant(ctx, id)
		return held, false, true, readErr
	case errors.Is(err, tenant.ErrAliasConflict):
		// The alias is held by a tenant whose identifier is not the derived
		// one. The deterministic pair therefore no longer names this account's
		// personal tenant, and writing the rest against it would attach the
		// account to someone else's row.
		return tenant.Tenant{}, false, true, fmt.Errorf("%w: the alias is held by another tenant: %w",
			errIdentifierUnavailable, err)
	default:
		return tenant.Tenant{}, false, true, fmt.Errorf("registering the personal tenant: %w", err)
	}
}

// convergeTenantAdminGrants brings the tenant-admin role's grants to the host's
// allowlist: a grant outside it is revoked, and a missing one is added. The
// seeding surface only adds, which is why the revocation reads the grants first.
func (p *personalProduct) convergeTenantAdminGrants(ctx context.Context, tenantID string) error {
	held, err := p.manager.Grants(ctx, tenantID, personalTenantAdminRole)
	if err != nil {
		return fmt.Errorf("reading the tenant-admin role's grants: %w", err)
	}
	allowed := make(map[authz.Grant]struct{}, len(personalTenantAdminPermissions()))
	grants := make([]authz.Grant, 0, len(personalTenantAdminPermissions()))
	for _, permission := range personalTenantAdminPermissions() {
		object, action, ok := p.catalog.ObjectAction(permission)
		if !ok {
			// The allowlist names a permission the process translation has no
			// row for, so no grant row can express it. Nothing is written for
			// it; the next call tries again in case a module adds the row.
			continue
		}
		grant := authz.Grant{Resource: object, Action: action}
		allowed[grant] = struct{}{}
		grants = append(grants, grant)
	}
	for _, grant := range held {
		if _, keep := allowed[authz.Grant{Resource: grant.Resource, Action: grant.Action}]; keep {
			continue
		}
		if err := p.manager.DeleteGrant(ctx, authz.GrantRecord{
			Tenant:   tenantID,
			Role:     personalTenantAdminRole,
			Resource: grant.Resource,
			Action:   grant.Action,
		}); err != nil {
			return fmt.Errorf("revoking a grant outside the tenant-admin role's allowlist: %w", err)
		}
	}
	if _, err := p.seeder.SeedGrants(ctx, tenantID, personalTenantAdminRole, grants); err != nil {
		return fmt.Errorf("seeding the tenant-admin role's grants: %w", err)
	}
	return nil
}

// personalTenantID derives the tenant identifier and alias from the account.
// The two are the same string by design: the alias is the request-surface name
// and the identifier is what the scoped tables hold.
func personalTenantID(account authn.AccountID) tenant.ID {
	return tenant.ID(personalIDPrefix + string(account))
}
