// Package admin is the administration module of the authentication capability:
// it mounts the management routes under the capability's scope bases, decides
// every one of them through the Decider seam, writes the mandatory audit before
// a write, and calls the corresponding capability.
//
// The module delivers no capability of its own. It is taken in only by a
// command that wants the management routes: without it the account, membership,
// session, factor, secret, SSO and impersonation capabilities are still
// available through their Go interfaces, and the host's own commands carry
// their access control. Taken in without the audit capability, the start fails
// before any instance is built, because a management action that could not be
// recorded is one this module refuses to take.
//
// Every route is guarded by the gateway. A route whose action is decided in the
// tenant domain is registered Impersonable: an impersonating identity reaches
// the handler and is decided by the effective account's permissions in that
// tenant, exactly as the account itself is. A platform-domain route is not
// registered, so the gateway answers an impersonating identity with a 401 there,
// and the handler checks the same domain again and answers 403
// impersonation-restricted without asking a Decider; a target account never
// holds a platform permission. The pipeline every handler shares is: the
// platform domain refuses an impersonating identity, a Decider is present, the
// path and body are well formed and agree on the tenant, the object's owner is
// resolved into the Decision, the object exists, the Decider allows it, the
// actor's authentication is strong enough, the mandatory audit lands, the
// capability is called. A read ends after the decision; a write that the
// capability refuses gets a failed audit row and the failure mapped to its
// answer.
//
// The groups of routes are mounted with the capabilities they need: the account
// and membership groups always, the session group with the session store, the
// factor and password group with the multi-factor module, the secret group with
// the token module, the SSO provider group with the SSO module, the metadata
// refresh group with an SSO metadata refresher, and the impersonation record
// group with the impersonation module. The SSO provider group carries one more
// condition, authn.admin.sso.enabled: the register capability says the process
// could manage providers, and the configuration item says this deployment
// offers that management surface, so the group is mounted only when both hold.
// A group whose capability is absent does not exist, and the route engine
// answers it as any unmatched path; the start diagnostics name the groups that
// were mounted.
package admin

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the management routes.
const ModuleName = "authn.admin"

// defaultEndpoint is the endpoint this module's platform-management fragment
// declares its routes for. The endpoint the routes are actually mounted on is
// the one the mounter resolves; the fragment is a static declaration written
// before that is known, so it carries the conventional name. The platform
// administration namespace lives on the admin endpoint, which claims
// /api/v1/system/tenants and /api/v1/system/auth alike. The tenant-management
// fragment names api, the conventional endpoint for routes decided in a
// tenant's policy domain.
const defaultEndpoint = "admin"

// adminFragment is this module's platform-domain OpenAPI fragment, describing
// the account and session groups and the platform's tenant-addressed
// impersonation-record group.
//
//go:embed openapi/admin.json
var adminFragment []byte

// adminTenantsFragment is this module's tenant-domain fragment, describing the
// membership and identity-provider groups.
//
//go:embed openapi/admin.tenants.json
var adminTenantsFragment []byte

// adminSpec and adminTenantsSpec are the two APIs this module's operations are
// declared against: the platform-domain surface and the tenant-domain one. The
// mounting resolves each declared route against the same root, so a path is
// written once.
var (
	adminSpec = speedhttp.Spec{
		Endpoint: defaultEndpoint, Prefix: "api", Version: "v1",
		Scope: speedhttp.ScopeSystem, Document: adminFragment,
	}
	adminTenantsSpec = speedhttp.Spec{
		Endpoint: "api", Prefix: "api", Version: "v1",
		Scope: speedhttp.ScopeTenants, Document: adminTenantsFragment,
	}
)

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers; it is the gateway's, so that a
// reader scanning for what authentication did finds both.
const diagnosticPrefix = "authn: "

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the management
// routes" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// The feature names this module declares. The account and membership groups are
// always mounted and publish the static name; every other name belongs to one
// group and is posted in the same judgement that mounts the group.
const (
	featureAdmin          = "authn.admin"
	featureSessions       = "authn.admin.sessions"
	featureFactors        = "authn.admin.factors"
	featureServiceKeys    = "authn.admin.secrets"
	featureSSO            = "authn.admin.sso"
	featureSSOMetadata    = "authn.admin.sso.metadata"
	featureImpersonations = "authn.admin.impersonations"
)

// Module is the descriptor a host registers.
//
// The route mounter, the account read and write surfaces, the membership reads,
// the signup verification reads and the audit capability are required, and each is delivered exclusively by
// another module of the directory, so the closure of a command that takes this
// one in does not pull them in: the host writes them down. The audit capability
// being required is what makes an assembly without one fail before any instance
// is built, rather than at the first write. The settings store is required as
// optional only so that it is initialised first. Every other capability and the
// decision seam are taken up in Init: the absence of a Decider is a legal
// assembly with the behaviour of refusing every request, and the absence of a
// capability is a group of routes that does not exist.
//
// The stance is StateEnabled and never StateAuto: whether to mount the
// management routes is a decision on the command tree and not a conclusion a
// configuration can infer. The one configuration item, authn.admin.sso.enabled,
// gates a single group inside the module rather than the module itself.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			configSchema(),
			adminSpec,
			adminTenantsSpec,
			feature.Spec{Name: featureAdmin},
			feature.Spec{Name: featureSessions, Conditional: true},
			feature.Spec{Name: featureFactors, Conditional: true},
			feature.Spec{Name: featureServiceKeys, Conditional: true},
			feature.Spec{Name: featureSSO, Conditional: true},
			feature.Spec{Name: featureSSOMetadata, Conditional: true},
			feature.Spec{Name: featureImpersonations, Conditional: true},
		},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			p.diagnose()
			return nil
		},

		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. The callbacks are
// also handed an instance a host supplied for the module, which can be any
// value that delivers the capability; this module delivers none, so a supplied
// instance is left alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It holds the required capabilities and the
// optional ones taken up in Init.
type product struct {
	mounter      mount.Mounter
	accounts     authn.Accounts
	accountAdm   authn.AccountAdmin
	passwords    authn.Passwords
	memberships  authn.Memberships
	verification authn.SignupVerification
	audit        audit.Audit

	// The optional capabilities. Each is nil when absent, and each gates the
	// group of routes that needs it.
	decider       authn.Decider
	sessions      authn.Sessions
	mfa           authn.MFA
	tokens        authn.Tokens
	ssoProviders  authn.SSOProviders
	domainClaims  domainClaims
	refreshers    []authn.SSOMetadataRefresher
	impersonation authn.Impersonation
	// records is the impersonation capability read through the extended shape
	// the record routes need; it is nil when the delivered capability does not
	// answer it.
	records records

	settings *reader
	logger   *slog.Logger

	// ssoGroupEnabled is the configuration gate of the SSO provider group. It is
	// read in New from authn.admin.sso.enabled and never changes after that.
	ssoGroupEnabled bool

	// board is the feature publisher, when this process has one. It is taken up
	// in Init and read in the same stage, never in New.
	board feature.Board

	// groups names the route groups that were mounted, for the start
	// diagnostic. It is written in Init and read in Start.
	groups []string
}

// newProduct takes up what the descriptor requires.
func newProduct(reg *core.Registry) (*product, error) {
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the route mounter: %w", diagnosticPrefix, ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account reads: %w", diagnosticPrefix, ModuleName, err)
	}
	accountAdm, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account writes: %w", diagnosticPrefix, ModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the membership reads: %w", diagnosticPrefix, ModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the signup verification reads: %w", diagnosticPrefix, ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the audit capability: %w", diagnosticPrefix, ModuleName, err)
	}
	configReader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(configReader)
	if err != nil {
		return nil, err
	}
	logger := log.Default()
	return &product{
		mounter:         mounter,
		accounts:        accounts,
		accountAdm:      accountAdm,
		memberships:     memberships,
		verification:    verification,
		audit:           auditor,
		logger:          logger,
		settings:        newReader(nil, logger),
		ssoGroupEnabled: cfg.SSO.Enabled,
	}, nil
}

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the start,
// because a capability that is there and cannot be taken up is not an absence.
func optional[T any](reg *core.Registry, what string) (T, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, nil
	default:
		return zero, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
	}
}

// wire runs in Init, where every instance exists. It takes up the optional
// capabilities, then mounts the routes of every group whose capability is
// there.
func (m *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		m.logger = delivered.Named(ModuleName)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return err
	}
	m.settings = newReader(store, m.logger)

	if m.decider, err = optional[authn.Decider](reg, "the decision seam"); err != nil {
		return err
	}
	if m.passwords, err = optional[authn.Passwords](reg, "the password capability"); err != nil {
		return err
	}
	if m.sessions, err = optional[authn.Sessions](reg, "the session store"); err != nil {
		return err
	}
	if m.mfa, err = optional[authn.MFA](reg, "the multi-factor composition"); err != nil {
		return err
	}
	if m.tokens, err = optional[authn.Tokens](reg, "the token capability"); err != nil {
		return err
	}
	if m.ssoProviders, err = optional[authn.SSOProviders](reg, "the SSO provider register"); err != nil {
		return err
	}
	if claims, ok := m.ssoProviders.(domainClaims); ok {
		m.domainClaims = claims
	}
	if m.impersonation, err = optional[authn.Impersonation](reg, "the impersonation capability"); err != nil {
		return err
	}
	if m.impersonation != nil {
		if r, ok := m.impersonation.(records); ok {
			m.records = r
		}
	}
	m.refreshers = core.ResolveAll[authn.SSOMetadataRefresher](reg)
	if b, err := core.Resolve[feature.Board](reg); err == nil {
		m.board = b
	}

	m.mountRoutes()
	return nil
}

// postFeature reports one conditional group name to the feature board, when the
// process has one. It is called in the same judgement that mounts the group.
func (m *product) postFeature(name string) {
	if m.board != nil {
		m.board.Post(ModuleName, name)
	}
}

// diagnose writes the start diagnostic: which route groups were mounted. The
// line is the module's own, so that a reader finds what the administration
// routes did without scanning a chain listing.
func (m *product) diagnose() {
	m.logger.Info(diagnosticPrefix + ModuleName + ": the management routes are mounted on the endpoint " +
		m.mounter.Endpoint() + " with the groups: " + m.groupList())
}

// groupList is the mounted groups as the diagnostic writes them, or "none" when
// the module mounted nothing (the account and membership groups are always
// mounted, so this is only reachable before Init).
func (m *product) groupList() string {
	if len(m.groups) == 0 {
		return "none"
	}
	out := ""
	for i, g := range m.groups {
		if i > 0 {
			out += ", "
		}
		out += g
	}
	return out
}
