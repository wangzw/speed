// Package admin is the administration module of the authentication capability:
// it mounts the management routes, decides every one through the Decider seam,
// audits each write before acting, and calls the corresponding capability.
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

// ModuleName is this module's name in the registry.
const ModuleName = "authn.admin"

const defaultEndpoint = "admin"

//go:embed openapi/admin.json
var adminFragment []byte

//go:embed openapi/admin.tenants.json
var adminTenantsFragment []byte

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

const diagnosticPrefix = "authn: "

func init() { core.ProcessRegistry.Register(Module()) }

const (
	featureAdmin          = "authn.admin"
	featureSessions       = "authn.admin.sessions"
	featureFactors        = "authn.admin.factors"
	featureServiceKeys    = "authn.admin.secrets"
	featureSSO            = "authn.admin.sso"
	featureSSOMetadata    = "authn.admin.sso.metadata"
	featureImpersonations = "authn.admin.impersonations"
)

// Module returns the descriptor a host registers. The route mounter, the account
// and membership surfaces, the signup verification reads and the audit
// capability are required.
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

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	mounter      mount.Mounter
	accounts     authn.Accounts
	accountAdm   authn.AccountAdmin
	passwords    authn.Passwords
	memberships  authn.Memberships
	verification authn.SignupVerification
	audit        audit.Audit

	decider       authn.Decider
	sessions      authn.Sessions
	mfa           authn.MFA
	tokens        authn.Tokens
	ssoProviders  authn.SSOProviders
	domainClaims  domainClaims
	refreshers    []authn.SSOMetadataRefresher
	impersonation authn.Impersonation
	roleReader    authn.MemberRoleReader
	records       records
	tenantNames   authn.TenantNameResolver

	settings *reader
	logger   *slog.Logger

	ssoGroupEnabled bool

	board feature.Board

	groups []string
}

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

func (m *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		m.logger = delivered.Named(ModuleName)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return err
	}
	m.settings = newReader(store, m.logger)

	if m.tenantNames, err = optional[authn.TenantNameResolver](reg, "the tenant name resolver"); err != nil {
		return err
	}
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
	if m.roleReader, err = optional[authn.MemberRoleReader](reg, "the member role reader"); err != nil {
		return err
	}
	m.refreshers = core.ResolveAll[authn.SSOMetadataRefresher](reg)
	if b, err := core.Resolve[feature.Board](reg); err == nil {
		m.board = b
	}

	m.mountRoutes()
	return nil
}

func (m *product) postFeature(name string) {
	if m.board != nil {
		m.board.Post(ModuleName, name)
	}
}

func (m *product) diagnose() {
	m.logger.Info(diagnosticPrefix + ModuleName + ": the management routes are mounted on the endpoint " +
		m.mounter.Endpoint() + " with the groups: " + m.groupList())
}

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
