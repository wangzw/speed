// Package account is the account store of the authentication capability: the
// accounts, the identifiers they are found by, their external identities and
// their tenant memberships, the password credentials and the secrets of service
// accounts, and the back-off that keeps guessing in check.
//
// One module, authn.account, owns the tables and delivers eight capabilities
// exclusively: Accounts and AccountAdmin, which read and write the store;
// Signups, which opens an account for anonymous self-registration, and
// SignupVerification, which reads how far that registration has got;
// Passwords, which checks and writes every credential of the password class;
// Memberships, which says which tenants an account belongs to;
// MembershipCleanup, which removes a membership on the account-deletion path
// without asking the guards; and Throttle, the database form of the failure
// back-off. The root package declares the interfaces, and every other module of
// this capability directory that needs an account takes them from the registry
// and never reads a table.
//
// An account is global and never reused. Ending one leaves a tombstone, so every
// reference to it from a session, an audit row or a role binding stays pointing
// at the same account. A creation and a state change are written in a transaction
// of this module and then announced to every AccountLifecycle hook, which builds
// or cleans what the other modules hold for the account; the account's status and
// its valid-after instant are what keep a session void when a cleanup has not
// finished.
//
// A host takes the module in by writing ModuleName into a command alongside a
// database engine. The command that applies the migrations has to take it in
// statically, or its tables do not exist and Start refuses.
package account

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/outbox"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.account"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = "authn.account"

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so the tree
// the database module sees has one directory per dialect at its root, which is
// the shape it reads.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// The capabilities this module delivers, each of which the product has to
// satisfy. The method names of the interfaces this capability provides are
// pairwise disjoint, which is what lets one instance satisfy all of them.
var (
	_ authn.Accounts           = (*product)(nil)
	_ authn.AccountAdmin       = (*product)(nil)
	_ authn.Signups            = (*product)(nil)
	_ authn.SignupVerification = (*product)(nil)
	_ authn.Passwords          = (*product)(nil)
	_ authn.Memberships        = (*product)(nil)
	_ authn.MembershipCleanup  = (*product)(nil)
	_ authn.Throttle           = (*product)(nil)
)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The database is required and is the one exclusive capability the module
// stands on: a command without a database has no account store, and the module
// that finds none stops the startup before anything is built. The eight
// capabilities are each delivered exclusively, so two providers of any of them
// is a startup failure and not two stores a read could land in.
//
// Prepare states that the module runs. Whether to run is the command's decision,
// and a command that names the module has promised it starts, so a configuration
// it cannot run on is a startup failure and there is no stand-down.
//
// The settings store, the event bus, the outbox, the audit capability, the
// metric capability and the logging capability are taken up in Init, where every
// instance exists, and none is required: without the settings every setting
// reads as its declared default, without a bus or an outbox no event is
// published, and without the rest nothing is recorded where it would have been.
// Every AccountLifecycle implementation is taken in the same stage and is not a
// requirement, so an assembly without a session, a factor or an SMS module still
// runs.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.Accounts)(nil), Exclusive: true},
			{Token: (*authn.AccountAdmin)(nil), Exclusive: true},
			{Token: (*authn.Signups)(nil), Exclusive: true},
			{Token: (*authn.SignupVerification)(nil), Exclusive: true},
			{Token: (*authn.Passwords)(nil), Exclusive: true},
			{Token: (*authn.Memberships)(nil), Exclusive: true},
			{Token: (*authn.MembershipCleanup)(nil), Exclusive: true},
			{Token: (*authn.Throttle)(nil), Exclusive: true},
		},
		Resources: []any{schema(), settingsSchema(), db.Migrations{FS: migrationsFS}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err = readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		// New builds the product and reads none of its tables: this stage runs
		// before the migrations are applied.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			return newProduct(database, cfg), nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.start(ctx)
		},

		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.stop(ctx)
			}
			return nil
		},
	}
}

// readerOf takes up the configuration reader. Its absence means the host never
// handed one to the registry, which is core's own failure and is carried out as it
// stands.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for this module, and the callbacks leave it
// alone: whatever it holds is released by whoever supplied it.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance.
type product struct {
	handle  *gorm.DB
	dialect db.Dialect
	cfg     resolved

	gov   *password.Governor
	decoy password.Decoy
	// signupGov is the capacity of the self-registration derivation: a width of
	// its own and no memory budget, so that registering never takes a place
	// from a password login and one derivation's memory is bounded by the
	// width. signupWait is how long a derivation that finds it full waits.
	signupGov  *password.Governor
	signupWait time.Duration
	// wait is how long a derivation that finds no capacity waits for it. It is a
	// field so that a case need not wait the second a real refusal takes.
	wait time.Duration
	// now is the clock every time written to the database or bound to a query is
	// read from, in UTC.
	now clock.Source
	// stopWait bounds how long Stop waits for the sweeping goroutines.
	stopWait time.Duration
	// sweepEvery is how often each table is swept.
	sweepThrottlesEvery, sweepSecretsEvery time.Duration

	// The optional capabilities, taken up in Init and read-only afterwards.
	store           settings.Store
	bus             event.Bus
	outbox          outbox.Outbox
	audit           audit.Audit
	hooks           []authn.AccountLifecycle
	membershipHooks []authn.MembershipLifecycle
	// membershipGuards are the optional checks an explicit membership removal
	// passes before it deletes the row, in module-name order. Every one has to
	// allow the removal; an empty list is the assembly with none.
	membershipGuards []authn.MembershipGuard
	meters           meters
	logger           *slog.Logger

	// mu guards reported, which holds for each fault the one last written, so that
	// a setting read on every request is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string

	sweepers sweepers
}

// newProduct builds the product from the configuration and the database.
func newProduct(database db.Database, cfg resolved) *product {
	return &product{
		handle:              database.DB(),
		dialect:             database.Dialect(),
		cfg:                 cfg,
		gov:                 password.NewGovernor(cfg.concurrency, cfg.memoryBudget),
		signupGov:           password.NewWidthGovernor(cfg.signupConcurrency),
		signupWait:          password.DefaultWait,
		wait:                password.DefaultWait,
		stopWait:            stopWait,
		sweepThrottlesEvery: throttleSweepEvery,
		sweepSecretsEvery:   secretSweepEvery,
		logger:              log.Default(),
		reported:            make(map[string]string),
	}
}

// wire takes up what is optional: the settings store, the event bus and the
// outbox, the audit capability, the metric capability, the logger and every
// lifecycle hook.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	var err error
	if p.store, err = optional[settings.Store](reg, "the settings store"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if p.outbox, err = optional[outbox.Outbox](reg, "the outbox"); err != nil {
		return err
	}
	if p.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	p.hooks = core.ResolveAll[authn.AccountLifecycle](reg)
	p.membershipHooks = core.ResolveAll[authn.MembershipLifecycle](reg)
	p.membershipGuards = core.ResolveAll[authn.MembershipGuard](reg)

	provider, err := optional[observability.MeterProvider](reg, "the metric capability")
	if err != nil {
		return err
	}
	if provider != nil {
		if p.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s: %w", ModuleName, err)
		}
	}
	if p.bus != nil && p.store != nil {
		p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
	}
	return nil
}

// optional takes up a capability the assembly may not carry. An absent provider
// is the zero value and no error; any other failure is carried out, since a
// capability that is there and cannot be taken up should not be left unused
// without a word.
func optional[T any](reg *core.Registry, what string) (T, error) {
	value, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return value, nil
	case errors.Is(err, core.ErrMissingProvider):
		var zero T
		return zero, nil
	default:
		var zero T
		return zero, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

// conn is the database handle bound to the context of one call.
func (p *product) conn(ctx context.Context) *gorm.DB { return p.handle.WithContext(ctx) }

// utc is a time as the database stores and compares it.
func utc(t time.Time) time.Time { return clock.UTC(t).Truncate(time.Microsecond) }

// at is the current time as the database stores it.
func (p *product) at() time.Time { return utc(p.now.Now()) }

// timePtr is a pointer to the time, which is nil for the zero time, the way a
// nullable column is written.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := utc(t)
	return &u
}

// timeOf is a nullable column read as the zero time when it is NULL.
func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return clock.UTC(*t)
}
