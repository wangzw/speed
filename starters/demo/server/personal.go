package main

// The personal-tenant host module: the demo host's own policy that an account
// with no membership of its own gets one personal tenant, so a registration
// completed through the sign-in surface can use the tenant area at once.
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

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	objectstoremeta "github.com/vislake/speed/pkg/objectstore/metadata"
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

// personalRoutePath is the route the ensure is served at. It hangs off the
// account endpoint's /api/v1/workspace claim; the prefix is written into every
// configuration and into the frontend's backend-prefix manifest, so it stays
// claimed even when the switch is off (the claim is configuration, the mounting
// is assembly: an off switch answers the prefix with 404).
const personalRoutePath = "/api/v1/workspace/personal"

// personalEndpoint is the listening endpoint the route is mounted on and the
// endpoint the OpenAPI fragment describes. It is fixed: the route is deliberately
// on the one endpoint that resolves no tenant, so that ensure can run before the
// caller has a tenant to resolve.
const personalEndpoint = "account"

// personalIDPrefix is the fixed prefix of every personal tenant identifier and
// alias. The rest is the account identifier, so the string is deterministic and
// carries no personal data.
const personalIDPrefix = "personal-"

// personalOwnerRole is the non-builtin role the ensure writes in the personal
// tenant. It is the tenant's owner, holding the allowlist below and nothing
// else.
const personalOwnerRole = authz.Role("owner")

// personalOwnerPermissions is the host-declared set of permissions the owner
// role holds. It is the personal workspace's business surface: the object
// store's two permissions today, and the notification surface declares no
// permission point. The set is written out in code and not derived from the
// process catalog, so a permission a module declares later does not join the
// owner role on its own.
//
// The authentication module's tenant-level management actions are deliberately
// absent — membership management, invitations, identity providers,
// impersonation, client management and non-member admission are decided in the
// tenant in the path, so granting them here would make every self-registered
// account an administrator of its tenant, able to add any known account to it
// and lock that account's own ensure out.
var personalOwnerPermissions = []authz.Permission{
	objectstoremeta.PermissionObjectRead,
	objectstoremeta.PermissionObjectWrite,
}

// personalOwnerDescriptionKey is the description key written with the owner
// role. It travels in the API answer wherever a role is listed; the demo renders
// no role surface of its own.
const personalOwnerDescriptionKey = "authz.role.personalOwner"

// personalMemberSource is the membership source the ensure writes. The account
// module publishes it on the membership event; "personal" names this host's own
// provisioning rather than an administrator, an invitation or an import.
const personalMemberSource = "personal"

// personalKind is the answer's tenant kind. The frontend renders the personal
// workspace label from it; the tenant record itself carries no display name.
const personalKind = "personal"

// The problem types the module writes. A 403, a 401 and a 503 carry no type of
// their own in this surface; the 409 does, because the frontend distinguishes
// the refusals a retry cannot change from a dependency failure.
const (
	personalProblemPrefix     = "urn:speed:workspace:"
	alreadyMemberCode         = "already-member"
	tenantUnavailableCode     = "tenant-unavailable"
	identifierUnavailableCode = "identifier-unavailable"
)

// The ensure's own refusals. None is returned by a dependency and so none
// carries a cause.
var (
	// errAlreadyMember refuses an account with no personal tenant of its own
	// that belongs to another tenant.
	errAlreadyMember = errors.New("the account already has a membership outside its personal tenant")
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

//go:embed personal-openapi.json
var personalFragment []byte

// init registers this module. The import is the wiring, as for every host
// module; the serve command's Include list is what puts it in a run.
func init() { core.ProcessRegistry.Register(personalModule()) }

// personalModule is the descriptor of this host's personal-tenant policy.
//
// The roster, the memberships, the policy manager, the seeding surface and the
// permission catalog are required: the ensure writes all five pieces, and an
// assembly that names this module without one of them cannot run it. The HTTP
// entry point, the feature board, the route-declaration surface and the logger
// are optional: without an entry point the routes are not mounted, without a
// board the conditional feature is simply absent from the document, and without
// the gateway's registration surface the route is guarded by the gateway's
// default and an impersonating caller is answered by the gateway instead of the
// handler.
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
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Seeder)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*feature.Board)(nil), Optional: true},
			{Token: (*authn.Gate)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			personalSchema(),
			speedhttp.Spec{Endpoint: personalEndpoint, Document: personalFragment},
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

// personalProduct is the module's instance: the five seams the ensure writes
// through and the switch it runs on.
type personalProduct struct {
	config      personalConfig
	roster      tenant.Catalog
	memberships authn.Memberships
	manager     authz.Manager
	seeder      authz.Seeder
	catalog     authz.Catalog
	logger      *slog.Logger
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
	p := &personalProduct{
		config:      cfg,
		roster:      roster,
		memberships: memberships,
		manager:     manager,
		seeder:      seeder,
		catalog:     catalog,
		logger:      log.Default(),
	}
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(personalModuleName)
	}
	return p, nil
}

// wire mounts the route and posts the feature when the switch is on. Every piece
// is idempotent-by-absence: an off switch or an assembly without an entry point
// mounts nothing, and the feature is posted only when the route is mounted.
func (p *personalProduct) wire(reg *core.Registry) error {
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
	endpoint.Route(http.MethodPost, personalRoutePath, p.handleEnsure)
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

// personalReply is the ensure's success answer.
type personalReply struct {
	Tenant  personalTenantView `json:"tenant"`
	Created bool               `json:"created"`
}

// personalTenantView is the tenant the answer names. It carries no display name:
// the frontend renders the personal label from Kind.
type personalTenantView struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
	Kind  string `json:"kind"`
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
// subject is not an account and is a 403, and an account that belongs to
// another tenant is the 409 already-member. An account whose personal tenant is
// not enabled is the 409 tenant-unavailable, and an identifier or alias the
// tenant cannot take is the 409 identifier-unavailable. Every dependency
// failure is a 503 that carries no part of the result.
func (p *personalProduct) handleEnsure(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
		return
	}
	if id.Impersonating() {
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
		return
	}
	if slices.Contains(id.Methods(), authn.MethodClientSecret) {
		// A service account authenticates only with its client secret, so the
		// method is the account kind: a non-interactive API client is given
		// tenant access by an administrator and gets no workspace of its own.
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
		return
	}
	account := id.Account()
	if account == "" || account.IsStatic() {
		// A subject of the static credential table has no account row, so it
		// cannot hold a membership or own a tenant.
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
		return
	}
	result, err := p.ensure(ctx, account)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, personalReply{
			Tenant: personalTenantView{
				ID:    string(result.Tenant.ID),
				Alias: result.Tenant.Alias,
				Kind:  personalKind,
			},
			Created: result.Created,
		})
	case errors.Is(err, errAlreadyMember):
		speedhttp.Fail(c, speedhttp.Problem{
			Status: http.StatusConflict,
			Type:   personalProblemPrefix + alreadyMemberCode,
		})
	case errors.Is(err, errTenantUnavailable):
		p.logger.Warn(personalModuleName+": the personal tenant is not enabled",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		speedhttp.Fail(c, speedhttp.Problem{
			Status: http.StatusConflict,
			Type:   personalProblemPrefix + tenantUnavailableCode,
		})
	case errors.Is(err, errIdentifierUnavailable):
		p.logger.Warn(personalModuleName+": the personal tenant's identifier is unavailable",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		speedhttp.Fail(c, speedhttp.Problem{
			Status: http.StatusConflict,
			Type:   personalProblemPrefix + identifierUnavailableCode,
		})
	case errors.Is(err, errRosterLagging):
		// The expected race: another call or another replica wrote the row and
		// this replica's snapshot has not caught up. It is retryable, not a
		// failure of the run, so it is not an error-level record.
		p.logger.Warn(personalModuleName+": the roster's snapshot lags the personal tenant row",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
	default:
		p.logger.Error(personalModuleName+": the personal tenant could not be ensured",
			"account", account.String(), "tenant", string(personalTenantID(account)), "cause", err.Error())
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
	}
}

// ensure checks and completes the five pieces of a personal tenant:
//
//  1. the tenant row (created, or read back when it already exists),
//  2. the account's membership in it,
//  3. the non-builtin owner role,
//  4. the owner role's grants, converged to the host's allowlist: the missing
//     ones added, the ones outside it revoked,
//  5. the account's binding to that role.
//
// Each step is idempotent, and a failure leaves the earlier steps in place for
// the next call to complete. What the ensure does not do is rewrite a decision
// an operator made: a membership row that exists is left as it stands, whatever
// its state, and a tenant row that is not enabled is answered with a refusal
// before anything is written.
//
// The eligibility check runs first. An account that already holds a membership
// in its personal tenant reads that tenant back, whether or not it belongs to
// other tenants too. An account with no personal membership and a membership
// elsewhere is refused and no tenant is created.
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

	memberships, err := p.memberships.Tenants(ctx, account)
	if err != nil {
		return personalResult{}, fmt.Errorf("reading the account's memberships: %w", err)
	}
	own, elsewhere := false, false
	for _, membership := range memberships {
		if membership.Tenant == string(personalID) {
			own = true
		} else {
			elsewhere = true
		}
	}
	if !own && elsewhere {
		return personalResult{}, errAlreadyMember
	}

	var record tenant.Tenant
	created := false
	if own {
		record, err = p.registeredTenant(ctx, personalID)
	} else {
		record, created, err = p.createTenant(ctx, personalID)
	}
	if err != nil {
		return personalResult{}, err
	}
	if record.Status != tenant.StatusActive {
		// A disabled tenant is the operator's decision. Nothing below runs, so
		// the next ensure leaves the row and its policy as they are.
		return personalResult{}, errTenantUnavailable
	}

	if !own {
		if err = p.memberships.Put(ctx, authn.Membership{
			Account: account,
			Tenant:  string(personalID),
			Active:  true,
			Source:  personalMemberSource,
		}); err != nil {
			return personalResult{}, fmt.Errorf("writing the membership: %w", err)
		}
	}

	roles, err := p.manager.Roles(ctx, string(personalID))
	if err != nil {
		return personalResult{}, fmt.Errorf("reading the personal tenant's roles: %w", err)
	}
	ownerPresent := false
	for _, role := range roles {
		if role.Role == personalOwnerRole {
			ownerPresent = true
			break
		}
	}
	if !ownerPresent {
		if err = p.manager.PutRole(ctx, authz.RoleRecord{
			Tenant:         string(personalID),
			Role:           personalOwnerRole,
			DescriptionKey: personalOwnerDescriptionKey,
			CreatedAt:      time.Now().UTC(),
		}); err != nil {
			return personalResult{}, fmt.Errorf("writing the owner role: %w", err)
		}
	}

	if err = p.convergeOwnerGrants(ctx, string(personalID)); err != nil {
		return personalResult{}, err
	}

	bindings, err := p.manager.BindingsOf(ctx, string(personalID), account.String())
	if err != nil {
		return personalResult{}, fmt.Errorf("reading the account's bindings: %w", err)
	}
	bound := false
	for _, binding := range bindings {
		if binding.Role == personalOwnerRole {
			bound = true
			break
		}
	}
	if !bound {
		if err := p.manager.PutBinding(ctx, authz.Binding{
			Tenant:    string(personalID),
			Account:   account.String(),
			Role:      personalOwnerRole,
			GrantedAt: time.Now().UTC(),
		}); err != nil {
			return personalResult{}, fmt.Errorf("binding the account to the owner role: %w", err)
		}
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
func (p *personalProduct) createTenant(ctx context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	record := tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	err := p.roster.Create(ctx, record)
	switch {
	case err == nil:
		return record, true, nil
	case errors.Is(err, tenant.ErrTenantExists):
		held, readErr := p.registeredTenant(ctx, id)
		return held, false, readErr
	case errors.Is(err, tenant.ErrAliasConflict):
		// The alias is held by a tenant whose identifier is not the derived
		// one. The deterministic pair therefore no longer names this account's
		// personal tenant, and writing the rest against it would attach the
		// account to someone else's row.
		return tenant.Tenant{}, false, fmt.Errorf("%w: the alias is held by another tenant: %w",
			errIdentifierUnavailable, err)
	default:
		return tenant.Tenant{}, false, fmt.Errorf("registering the personal tenant: %w", err)
	}
}

// convergeOwnerGrants brings the owner role's grants to the host's allowlist:
// a grant outside it is revoked, and a missing one is added. The seeding
// surface only adds, which is why the revocation reads the grants first.
func (p *personalProduct) convergeOwnerGrants(ctx context.Context, tenantID string) error {
	held, err := p.manager.Grants(ctx, tenantID, personalOwnerRole)
	if err != nil {
		return fmt.Errorf("reading the owner role's grants: %w", err)
	}
	allowed := make(map[authz.Grant]struct{}, len(personalOwnerPermissions))
	grants := make([]authz.Grant, 0, len(personalOwnerPermissions))
	for _, permission := range personalOwnerPermissions {
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
			Role:     personalOwnerRole,
			Resource: grant.Resource,
			Action:   grant.Action,
		}); err != nil {
			return fmt.Errorf("revoking a grant outside the owner role's allowlist: %w", err)
		}
	}
	if _, err := p.seeder.SeedGrants(ctx, tenantID, personalOwnerRole, grants); err != nil {
		return fmt.Errorf("seeding the owner role's grants: %w", err)
	}
	return nil
}

// personalTenantID derives the tenant identifier and alias from the account.
// The two are the same string by design: the alias is the request-surface name
// and the identifier is what the scoped tables hold.
func personalTenantID(account authn.AccountID) tenant.ID {
	return tenant.ID(personalIDPrefix + string(account))
}
