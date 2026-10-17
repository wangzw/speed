// Package impersonation is the impersonation module of the authentication
// capability: it holds the impersonation records, decides whether one may begin,
// writes the mandatory audit before the action, and answers whether a record is
// still in force.
//
// An impersonation is a record with a lifetime, a reason and one tenant, and not
// a new credential and not a switch on an identity. It hangs on the
// administrator's own session; the browser carries it as a marker on that
// session, and the API carries it as a short-lived impersonation access token.
// The administrator stays the accountable party through every request: the
// identity's account is the target and its actor and key are the administrator.
//
// The module enforces a set of structural restrictions that do not depend on the
// decision seam: who the actor is, how recently and how strongly the actor
// authenticated, which target and which tenant, how long, and whether the actor's
// session already has one in force. A Decider is asked above those and can only
// tighten them, never relax them. The result is a record that is never deleted,
// that answers who impersonated whom, and that carries the module's seam-level
// audit rows: a start is written only when the mandatory audit succeeded.
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

// defaultEndpoint is the endpoint this module's own OpenAPI fragment declares its
// routes for. The endpoint the routes are actually mounted on is the one the
// mounter resolves; the fragment is a static declaration written before that is
// known, so it carries the conventional name.
const defaultEndpoint = "account"

// impersonationFragment is this module's OpenAPI fragment, describing the three
// routes of the impersonation self-service.
//
//go:embed openapi/impersonation.json
var impersonationFragment []byte

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so that the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive is what makes this unreachable: the directory is
		// part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// The module registers itself. Importing this package is what puts it in a
// binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// product must deliver what the descriptor declares.
var _ authn.Impersonation = (*product)(nil)

// Module is the descriptor a host registers.
//
// The database, the route mounter, the session store, the account reads, the
// membership reads, the multi-factor composition, the audit capability and the
// tenant admission layer's scope guard are required, and each is delivered
// exclusively by another module of the directory, so the closure of a command
// that takes this one in does not pull them in: the host writes them down. The
// settings store is required as optional only so that it is initialised first;
// the decision seam, the token capability, the event bus, the logger and the
// metric provider are taken up in Init, and the absence of a Decider is a legal
// assembly with the behaviour of refusing every start.
//
// The stance is StateEnabled and never StateAuto: whether to run is a decision
// on the command tree and not a conclusion a configuration can infer.
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
			speedhttp.Spec{Endpoint: defaultEndpoint, Document: impersonationFragment},
			// The impersonation self-service routes: mounted whenever the module
			// is in force, so the feature is static.
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

		// The handle belongs to the database module, which closes it.
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied for the module, which can be any value that
// delivers the capability; they act on the module's own product and leave a
// supplied one as it is.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It delivers the impersonation capability.
type product struct {
	store       *store
	accounts    authn.Accounts
	memberships authn.Memberships
	mfa         authn.MFA
	sessions    authn.Sessions
	audit       audit.Audit
	scopeGuard  authn.ScopeGuard
	mounter     mount.Mounter

	// What Init takes up. decider and tokens are nil when absent, which the
	// behaviour of each states.
	decider  authn.Decider
	tokens   authn.Tokens
	bus      event.Bus
	meters   *meters
	settings *reader
	logger   *slog.Logger
	clock    clock.Source
}

// newProduct takes up what the descriptor requires.
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
	return build(database.DB(), accounts, memberships, mfa, sessions, auditor, guard, mounter), nil
}

// build assembles a product from its parts. The optional parts start absent and
// the logger starts as the process's, so that a product that has not been through
// Init is still one that does not panic.
func build(handle *gorm.DB, accounts authn.Accounts, memberships authn.Memberships, mfa authn.MFA,
	sessions authn.Sessions, auditor audit.Audit, guard authn.ScopeGuard, mounter mount.Mounter,
) *product {
	logger := log.Default()
	return &product{
		store:       &store{handle: handle},
		accounts:    accounts,
		memberships: memberships,
		mfa:         mfa,
		sessions:    sessions,
		audit:       auditor,
		scopeGuard:  guard,
		mounter:     mounter,
		meters:      &meters{},
		settings:    newReader(nil, logger),
		logger:      logger,
	}
}

// wire runs in Init, where every instance exists. It takes up the optional
// capabilities and mounts the routes.
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

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the startup,
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

// start runs in Start: the table must be there, and the mount endpoint must not
// be one the admission layer guards, or an impersonating identity would be
// answered 404 there and could not even end its impersonation.
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
