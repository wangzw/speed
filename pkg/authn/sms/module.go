// Package sms is the SMS module of the authentication capability: the whole
// state machine of a verification code, whatever its purpose. It writes the
// challenge row, spends the budgets, counts the attempts, consumes the code and
// hands the sending to a carrier through the sending seam.
//
// A purpose is one of the closed set login, mfa, enroll and reset: the first
// factor of a login, the second factor, the proof of a number during enrollment,
// and the proof of a number during an anonymous password reset. The state of a
// challenge is one table whatever the purpose, so the code is held once and the
// consumers — the SMS login method, the SMS factor, the phone routes of the login
// engine and its password reset route — hold none of it. Only login and reset
// send a code without an account: neither counts against the per-account budget,
// and a reset row fills no account at all.
//
// The module delivers authn.SMSCodes exclusively and the authn.AccountLifecycle
// hook beside it, as the module that holds data keyed by an account, and it
// publishes the optional authn.PurposeSupport so a caller can learn whether the
// carrier declares the reset template. It takes
// the sending seam sms.Sender and the template registry sms.Registry as
// exclusive requirements: a command tree names one of each, and two at once fail
// in resolution. The registry decides which template a message goes out under; a
// carrier module does the constructing, calling and error classification alone;
// and this module checks the templates it needs against what the registry
// declares before it starts.
//
// The plaintext code never leaves the module: it is written to no table, no
// log, no event and no metric, and lives only in the queued message and the
// call to the sender. The number is not stored either; the searchable column
// holds its keyed digest.
package sms

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command whose users receive verification codes by SMS.
const ModuleName = "authn.sms"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name. The settings this module declares hang from the same namespace.
const ConfigNamespace = ModuleName

// challengeTable is the challenge table this module owns.
const challengeTable = "authn_challenges"

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
		// The embed directive above makes this unreachable: the directory is
		// part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package is
// what puts the SMS capability in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver what the descriptor declares.
var (
	_ authn.SMSCodes         = (*product)(nil)
	_ authn.AccountLifecycle = (*product)(nil)
	_ authn.PurposeSupport   = (*product)(nil)
)

// Module is the descriptor a host registers.
//
// The database, the template registry and the sending seam are required, and
// each is delivered exclusively by another module, so the closure of a command
// that names this one does not pull them in: the host writes them down. The
// registry and the sending seam are functional, exclusive requirements: a
// command that names no provider delivering one fails in resolution, and two at
// once conflict. The registry is taken up before the sending seam, because this
// module checks its templates against what the registry declares and hands the
// carrier only what the registry resolved.
//
// The settings store is required as optional only so that it is initialised
// before this one and its snapshot is loaded when Init reads the settings; the
// event bus, the audit capability, the logging capability and the metric
// capability are taken up in Init, and each has a stated behaviour for its
// absence, which is a legal assembly.
//
// The stance is StateEnabled and never StateAuto: the module claims the SMSCodes
// capability exclusively, and an exclusive provider that states StateAuto is the
// cell resolution disables.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*sms.Registry)(nil)},
			{Token: (*sms.Sender)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.SMSCodes)(nil), Exclusive: true},
			{Token: (*authn.AccountLifecycle)(nil)},
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

		// New takes up what the descriptor requires and reads no table of its
		// own: this stage runs before the migrations are applied, so on a first
		// start the table is not there yet.
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

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.start(ctx)
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				return p.stop()
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

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the worker pool is sized from configuration, and no reader was handed "+
			"to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// product is the module's instance. It delivers the SMS capability and the
// account lifecycle hook.
type product struct {
	database db.Database
	handle   *gorm.DB
	registry sms.Registry
	sender   sms.Sender

	// defaultLocale is the language a request that names none resolves to, read
	// from the shared i18n item. localesByTemplate is, per template name, the
	// languages that template declares and a message under it may be sent in.
	// Both are read once, at construction, because the configuration is a
	// snapshot.
	defaultLocale     string
	localesByTemplate map[string][]string
	// resetSupported is true when the carrier declared the authn.code.reset
	// template, which is what makes the reset purpose available. It is derived
	// from the same declaration that produced localesByTemplate.
	resetSupported bool

	register *register
	pool     *pool
	meters   *meters

	// What Init takes up. They are read-only after Init.
	settings *reader
	bus      event.Bus
	audit    audit.Audit
	logger   *slog.Logger

	clock clock.Source
	seen  *seenEvents

	sweeper    *sweeper
	sweepEvery time.Duration
	stopBound  time.Duration
}

// newProduct takes up what the descriptor requires and assembles the product.
// The worker pool is sized here, from configuration, which is why the reader is
// required; the default language and the templates' languages are read here too,
// because the registry's declaration is available only once the registry is
// taken up.
func newProduct(reg *core.Registry) (*product, error) {
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
		return nil, fmt.Errorf("%s%s: taking up the database: %w", diagnosticPrefix, ModuleName, err)
	}
	registry, err := core.Resolve[sms.Registry](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the template registry: %w", diagnosticPrefix, ModuleName, err)
	}
	sender, err := core.Resolve[sms.Sender](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the sending seam: %w", diagnosticPrefix, ModuleName, err)
	}
	// The default language is the shared i18n item, so a run reads one statement
	// of it whatever engine is assembled; an assembly with no i18n engine keeps
	// the fallback.
	defaultLocale, err := readDefaultLocale(reader)
	if err != nil {
		return nil, fmt.Errorf("%s%s: reading the default language: %w", diagnosticPrefix, ModuleName, err)
	}
	// The registry is taken up here, so the templates in force are checked here:
	// Prepare runs earlier and holds no instance. A registry that does not cover
	// the templates this module sends under, or the configured default language,
	// stops the startup, before any code.
	declared := registry.Declared()
	if err := checkSender(declared, defaultLocale); err != nil {
		return nil, fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	return build(database, registry, sender, cfg, defaultLocale, templateLocales(declared)), nil
}

// build assembles a product from its parts. The optional parts start absent and
// the logger starts as the process's, so that a product that has not been through
// Init is still one that does not panic.
func build(database db.Database, registry sms.Registry, sender sms.Sender, cfg resolved, defaultLocale string, localesByTemplate map[string][]string) *product {
	logger := log.Default()
	// templateLocales writes an entry for the reset template only when the
	// carrier declared it, so its presence is the support answer.
	_, resetSupported := localesByTemplate[templateReset]
	return &product{
		database:          database,
		handle:            database.DB(),
		registry:          registry,
		sender:            sender,
		defaultLocale:     defaultLocale,
		localesByTemplate: localesByTemplate,
		resetSupported:    resetSupported,
		register:          &register{handle: database.DB(), dialect: database.Dialect()},
		pool:              newPool(cfg.Workers, cfg.QueueSize),
		meters:            &meters{},
		settings:          newReader(nil, logger),
		logger:            logger,
		seen:              newSeenEvents(),
		sweepEvery:        sweepInterval,
		stopBound:         stopWait,
	}
}

// wire runs in Init, where every instance exists: it takes up the optional
// capabilities and subscribes to the two events the module follows.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return err
	}
	p.settings = newReader(store, p.logger)

	if p.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if provider, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if provider != nil {
		if p.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s%s: creating the instruments: %w", diagnosticPrefix, ModuleName, err)
		}
	}

	if p.bus != nil {
		p.subscribe(p.bus)
	}
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
