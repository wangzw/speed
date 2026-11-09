// Package mfa is the multi-factor module of the authentication capability: the
// register of the second factors an account has, the policy that says whether a
// login needs one, the orchestration around every call to a factor, and the one
// place the assurance of a login is composed.
//
// The kinds of factor are not here. A kind is a module of its own that delivers
// authn.Factor beside this one, and this module takes them all with
// core.ResolveAll and imports none of them. What a factor does is cryptography
// and the state of its own; the back-off after a failure, the audit row and the
// event are done here, once, for every kind, so that a kind that forgot them does
// not exist.
//
// The module is the multi-factor capability's exclusive provider and delivers the
// account lifecycle hook beside it, as the module that holds data keyed by an
// account.
package mfa

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"time"

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
// command that takes multi-factor authentication in.
const ModuleName = "authn.mfa"

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

// admissionModule is the name of the module that enforces a tenant's requirement
// of multi-factor at request time. It is a string and not an import: the
// authentication directory does not import the tenant directory.
const admissionModule = "tenant.admission"

// defaultEndpoint is the endpoint this module's fragment is declared on: the
// account endpoint under the gateway's base path, which is where the reference
// assembly mounts the authentication capability's own routes. A resource is a
// static declaration fixed before configuration is read, so the field carries
// this default; the merged document takes it as the endpoint of these
// operations. A deployment that mounts the routes elsewhere is drift, which the
// fragment is allowed to have and which produces no failure signal (see pkg/ops).
const defaultEndpoint = "account"

//go:embed openapi/mfa.json
var mfaFragment []byte

// mfaSpec is the API root this module's fragment and routes hang off. Its
// prefix and version are the common ones; the fragment writes the capability's
// own segment, so a route's declared path begins with the part of the mounter's
// base that lies below this root.
var mfaSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: mfaFragment,
}

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
var (
	_ authn.MFA              = (*product)(nil)
	_ authn.AccountLifecycle = (*product)(nil)
)

// Module is the descriptor a host registers.
//
// The database, the route mounter, the account reads and writes and the failure
// back-off are required, and each is delivered exclusively by another module of
// the directory, so the closure of a command that takes this one in does not pull
// them in: the host writes them down. The settings store is required as optional
// only so that it is initialised first and its snapshot is loaded when Init reads
// the one setting a startup check needs; everything else optional is taken up in
// Init, and its absence is a legal assembly with the behaviour each method states.
//
// The stance is StateEnabled and never StateAuto: the module claims the
// capability exclusively, and an exclusive provider that states StateAuto is the
// cell resolution disables.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*authn.Throttle)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.MFA)(nil), Exclusive: true},
			{Token: (*authn.AccountLifecycle)(nil)},
		},
		Resources: []any{
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
			mfaSpec,
			// The multi-factor self-service surface: the two guarded routes the
			// module mounts, which are present whenever it is in force.
			feature.Spec{Name: "authn.mfa"},
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
			return p.start(ctx, reg)
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				return p.stopSweep()
			}
			return nil
		},

		// The handle belongs to the database module, which closes it.
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied for the module, which can be any value that
// delivers the capabilities; they act on the module's own product and leave a
// supplied one as it is.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It delivers the multi-factor capability and
// the account lifecycle hook.
type product struct {
	register *register
	accounts authn.Accounts
	admin    authn.AccountAdmin
	throttle authn.Throttle
	mounter  mount.Mounter

	// What Init takes up. factors is read-only after Init.
	factors map[authn.FactorKind]authn.Factor
	kinds   []authn.FactorKind
	// tenantMFA is the seam that answers whether a tenant any member belongs to
	// requires multi-factor. It is nil in an assembly that wires none, and then
	// only the account and deployment policies decide.
	tenantMFA authn.TenantMFARequirement
	sessions  authn.Sessions
	audit     audit.Audit
	bus       event.Bus
	meters    *meters
	settings  *reader
	logger    *slog.Logger

	clock clock.Source
	seen  *seenEvents

	sweeper    *sweeper
	sweepEvery time.Duration
	stopBound  time.Duration
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
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account reads: %w", diagnosticPrefix, ModuleName, err)
	}
	admin, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account writes: %w", diagnosticPrefix, ModuleName, err)
	}
	throttle, err := core.Resolve[authn.Throttle](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the failure back-off: %w", diagnosticPrefix, ModuleName, err)
	}
	return build(database.DB(), accounts, admin, throttle, mounter), nil
}

// build assembles a product from its parts. The optional parts start absent and
// the logger starts as the process's, so that a product that has not been through
// Init is still one that does not panic.
func build(handle *gorm.DB, accounts authn.Accounts, admin authn.AccountAdmin, throttle authn.Throttle, mounter mount.Mounter) *product {
	logger := log.Default()
	return &product{
		register:   &register{handle: handle},
		accounts:   accounts,
		admin:      admin,
		throttle:   throttle,
		mounter:    mounter,
		factors:    map[authn.FactorKind]authn.Factor{},
		meters:     &meters{},
		settings:   newReader(nil, logger),
		logger:     logger,
		seen:       newSeenEvents(),
		sweepEvery: sweepInterval,
		stopBound:  stopWait,
	}
}

// wire runs in Init, where every instance exists. It takes up the factors and
// holds them to what the module needs of them, takes up the optional capabilities,
// subscribes to the account deletion and mounts the routes.
func (m *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		m.logger = delivered.Named(ModuleName)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return err
	}
	m.settings = newReader(store, m.logger)

	if err = m.takeFactors(core.ResolveAll[authn.Factor](reg)); err != nil {
		return err
	}
	if err = m.checkFactorsForPolicy(); err != nil {
		return err
	}

	if m.sessions, err = optional[authn.Sessions](reg, "the session store"); err != nil {
		return err
	}
	if m.tenantMFA, err = optional[authn.TenantMFARequirement](reg, "the tenant multi-factor requirement"); err != nil {
		return err
	}
	if m.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	if m.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if m.bus != nil {
		m.subscribe(m.bus)
	}
	if provider, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if provider != nil {
		if m.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s%s: creating the instruments: %w", diagnosticPrefix, ModuleName, err)
		}
	}

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

// checkFactorsForPolicy holds the factors taken in to the policy of the
// deployment: a deployment that requires a factor of every account has to have a
// kind for them to register. The read of the switch that fails is a failure of
// the startup, the direction a mandatory switch that cannot be read goes.
func (m *product) checkFactorsForPolicy() error {
	if len(m.factors) > 0 {
		return nil
	}
	required, err := m.settings.globalRequired()
	if err != nil {
		return fmt.Errorf("%s%s: reading %s: %w", diagnosticPrefix, ModuleName, globalRequiredPath, err)
	}
	if required {
		return fmt.Errorf("%w: %s is set, and no module delivering a factor is taken in. Write the module "+
			"of at least one factor kind into this command, or leave the setting off",
			authn.ErrNoFactor, globalRequiredPath)
	}
	return nil
}

// takeFactors indexes the factors by kind. A kind outside the closed set is a
// declaration that is not well formed, and two factors of one kind are
// ErrFactorConflict naming both.
func (m *product) takeFactors(all []authn.Factor) error {
	for _, f := range all {
		kind := f.Kind()
		if !kindValid(kind) {
			return fmt.Errorf("%w: the factor %T declares the kind %q, which is not one of %s",
				authn.ErrInvalidClaim, f, kind, kindList(allKinds()))
		}
		if prior, dup := m.factors[kind]; dup {
			return fmt.Errorf("%w: the kind %q is delivered by both %T and %T. One module delivers one kind; "+
				"leave one of them out of this command", authn.ErrFactorConflict, kind, prior, f)
		}
		m.factors[kind] = f
	}
	m.kinds = make([]authn.FactorKind, 0, len(m.factors))
	for kind := range m.factors {
		m.kinds = append(m.kinds, kind)
	}
	slices.Sort(m.kinds)
	return nil
}

func allKinds() []authn.FactorKind {
	return []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS, authn.FactorWebAuthn}
}

func kindList(kinds []authn.FactorKind) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

// start runs in Start: the table must be there, the register must agree with
// the factors taken in, the diagnostics are written, and the sweep begins.
func (m *product) start(ctx context.Context, reg *core.Registry) error {
	if err := m.register.probe(ctx); err != nil {
		return err
	}
	inUse, err := m.register.activeKinds(ctx)
	if err != nil {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	if len(m.factors) == 0 && len(inUse) > 0 {
		return fmt.Errorf("%w: accounts hold active factors of the kinds %s, and no module delivering a factor "+
			"is taken in, so no login that requires one could be completed. Write the modules of those kinds "+
			"into this command", authn.ErrNoFactor, kindList(inUse))
	}
	m.diagnose(inUse)
	if !reg.Participating(admissionModule) {
		m.logger.Warn(diagnosticPrefix + admissionModule + " is not taking part, so a tenant's requirement of " +
			"multi-factor authentication is enforced by nobody")
	}
	m.startSweep(ctx)
	return nil
}

// diagnose writes the factor kinds taking part, and a line for each kind accounts
// hold that has no implementation here, which is a factor that is unavailable.
func (m *product) diagnose(inUse []authn.FactorKind) {
	if len(m.kinds) == 0 {
		m.logger.Info(diagnosticPrefix + "no factor kind takes part in multi-factor authentication")
	} else {
		m.logger.Info(diagnosticPrefix + "the factor kinds taking part: " + kindList(m.kinds))
	}
	for _, kind := range inUse {
		if _, ok := m.factors[kind]; !ok {
			m.logger.Warn(diagnosticPrefix+"accounts hold active factors of the kind "+string(kind)+
				", which no module taken in delivers, so those factors are unavailable and a login that needs "+
				"one of them cannot be completed", slog.String("factor", string(kind)))
		}
	}
}
