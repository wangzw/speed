// Package impersonation holds impersonation records: it decides whether one may
// begin, audits it, and reports whether a record is still in force.
package impersonation

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that takes impersonation in.
const ModuleName = "authn.impersonation"

// defaultEndpoint is the conventional endpoint the static fragment declares; the
// mounter decides where the routes are actually mounted.
const defaultEndpoint = "account"

//go:embed openapi/impersonation.json
var impersonationFragment []byte

var impersonationSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: impersonationFragment,
}

const diagnosticPrefix = "authn: "

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

var _ authn.Impersonation = (*product)(nil)

// Module is the descriptor a host registers. Its required capabilities are the
// database, the route mounter, the session and account stores, the signup and
// membership reads, multi-factor, audit and the tenant scope guard.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Sessions)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.MFA)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.ScopeGuard)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.Impersonation)(nil), Exclusive: true},
		},
		Resources: []any{
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
			impersonationSpec,
			operations,
			feature.Spec{Name: "authn.impersonation"},
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

		Start: func(ctx context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.start(ctx)
		},

		Stop: func(context.Context, *core.Registry, any) error { return nil },

		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	store        *store
	accounts     authn.Accounts
	verification authn.SignupVerification
	memberships  authn.Memberships
	mfa          authn.MFA
	sessions     authn.Sessions
	audit        audit.Audit
	scopeGuard   authn.ScopeGuard
	mounter      mount.Mounter

	decider     authn.Decider
	tokens      authn.Tokens
	bus         event.Bus
	meters      *meters
	settings    *reader
	tenantNames authn.TenantNameResolver
	// tenantDirectory maps a tenant name to its identifier; with no directory a
	// start cannot resolve its tenant and is refused.
	tenantDirectory authn.TenantDirectory
	logger          *slog.Logger
	clock           clock.Source
}

func newProduct(reg *core.Registry) (*product, error) {
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the database: %w", diagnosticPrefix, ModuleName, err)
	}
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the route mounter: %w", diagnosticPrefix, ModuleName, err)
	}
	sessions, err := core.Resolve[authn.Sessions](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the session store: %w", diagnosticPrefix, ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account reads: %w", diagnosticPrefix, ModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the signup verification reads: %w", diagnosticPrefix, ModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the membership reads: %w", diagnosticPrefix, ModuleName, err)
	}
	mfa, err := core.Resolve[authn.MFA](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the multi-factor composition: %w", diagnosticPrefix, ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the audit capability: %w", diagnosticPrefix, ModuleName, err)
	}
	guard, err := core.Resolve[authn.ScopeGuard](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the tenant scope guard: %w", diagnosticPrefix, ModuleName, err)
	}
	return build(database.DB(), accounts, verification, memberships, mfa, sessions, auditor, guard, mounter), nil
}

func build(handle *gorm.DB, accounts authn.Accounts, verification authn.SignupVerification,
	memberships authn.Memberships, mfa authn.MFA, sessions authn.Sessions, auditor audit.Audit,
	guard authn.ScopeGuard, mounter mount.Mounter,
) *product {
	logger := log.Default()
	return &product{
		store:        &store{handle: handle},
		accounts:     accounts,
		verification: verification,
		memberships:  memberships,
		mfa:          mfa,
		sessions:     sessions,
		audit:        auditor,
		scopeGuard:   guard,
		mounter:      mounter,
		meters:       &meters{},
		settings:     newReader(nil, logger),
		logger:       logger,
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

	if m.decider, err = optional[authn.Decider](reg, "the decision seam"); err != nil {
		return err
	}
	if m.tokens, err = optional[authn.Tokens](reg, "the token capability"); err != nil {
		return err
	}
	if m.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if m.tenantNames, err = optional[authn.TenantNameResolver](reg, "the tenant name resolver"); err != nil {
		return err
	}
	if m.tenantDirectory, err = optional[authn.TenantDirectory](reg, "the tenant directory"); err != nil {
		return err
	}
	if provider, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if provider != nil {
		if m.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s%s: creating the instruments: %w", diagnosticPrefix, ModuleName, err)
		}
	}

	m.subscribeSettings(m.bus)

	m.mountRoutes()
	return nil
}

// optional reads a capability whose absence is legal; any failure other than
// ErrMissingProvider is returned, because a capability that is there and cannot
// be read is not an absence.
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

func (m *product) start(ctx context.Context) error {
	if err := m.store.probe(ctx); err != nil {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	endpoint := m.mounter.Endpoint()
	if m.scopeGuard.Covers(endpoint) {
		return fmt.Errorf("%w: the impersonation routes are mounted on the endpoint %q, which the tenant "+
			"admission layer guards; mount them on another endpoint", authn.ErrInvalidConfig, endpoint)
	}
	m.logger.Info(diagnosticPrefix + ModuleName + ": the impersonation routes are mounted on the endpoint " + endpoint)
	return nil
}
