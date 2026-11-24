// Package sms is the SMS module: the state machine of a verification code for the login, mfa, enroll and reset purposes, held in one table and one shared ledger, with sending handed to a carrier. The plaintext code and the number never leave the module, and the human-verification gate is checked before any budget is spent.
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
	"github.com/vislake/speed/pkg/sms/budget"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command whose users receive verification codes by SMS.
const ModuleName = "authn.sms"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name. The settings this module declares hang from the same namespace.
const ConfigNamespace = ModuleName

const challengeTable = "authn_challenges"

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

var (
	_ authn.SMSCodes         = (*product)(nil)
	_ authn.AccountLifecycle = (*product)(nil)
	_ authn.PurposeSupport   = (*product)(nil)
)

// Module is the descriptor a host registers. It requires the database, the template registry, the sending seam, the shared ledger and the human-verification gate, takes the settings store as optional, and claims authn.SMSCodes exclusively with the enabled stance.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*sms.Registry)(nil)},
			{Token: (*sms.Sender)(nil)},
			{Token: (*budget.Store)(nil)},
			{Token: (*authn.HumanVerifier)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.SMSCodes)(nil), Exclusive: true},
			{Token: (*authn.AccountLifecycle)(nil)},
		},
		Resources: []any{schema(), settingsSchema(), db.Migrations{FS: migrationsFS}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			off, reason, err := sms.OffBySelection(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the SMS carrier selection: %w", ModuleName, err)
			}
			if off {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err = readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			missing, reason, err := sms.OffByMissingTemplates(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the SMS template set: %w", ModuleName, err)
			}
			if missing {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
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

		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the worker pool is sized from configuration, and no reader was handed "+
			"to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

type product struct {
	database db.Database
	handle   *gorm.DB
	registry sms.Registry
	sender   sms.Sender

	defaultLocale     string
	localesByTemplate map[string][]string
	resetSupported    bool

	register *register
	ledger   budget.Store
	pool     *pool
	meters   *meters

	settings *reader
	bus      event.Bus
	audit    audit.Audit
	logger   *slog.Logger
	human    authn.HumanVerifier

	clock clock.Source
	seen  *seenEvents

	sweeper    *sweeper
	sweepEvery time.Duration
	stopBound  time.Duration
}

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
	ledger, err := core.Resolve[budget.Store](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the shared SMS ledger: %w", diagnosticPrefix, ModuleName, err)
	}
	human, err := core.Resolve[authn.HumanVerifier](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the human-verification gate: %w", diagnosticPrefix, ModuleName, err)
	}
	defaultLocale, err := readDefaultLocale(reader)
	if err != nil {
		return nil, fmt.Errorf("%s%s: reading the default language: %w", diagnosticPrefix, ModuleName, err)
	}
	declared := registry.Declared()
	if err := checkSender(declared, defaultLocale); err != nil {
		return nil, fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	p := build(database, registry, sender, ledger, cfg, defaultLocale, templateLocales(declared))
	p.human = human
	return p, nil
}

func build(database db.Database, registry sms.Registry, sender sms.Sender, ledger budget.Store, cfg resolved, defaultLocale string, localesByTemplate map[string][]string) *product {
	logger := log.Default()
	// The reset template's entry exists only when the carrier declared it, so its presence is the support answer.
	_, resetSupported := localesByTemplate[templateReset]
	register := &register{handle: database.DB(), dialect: database.Dialect()}
	return &product{
		database:          database,
		handle:            database.DB(),
		registry:          registry,
		sender:            sender,
		defaultLocale:     defaultLocale,
		localesByTemplate: localesByTemplate,
		resetSupported:    resetSupported,
		register:          register,
		ledger:            ledger,
		pool:              newPool(cfg.Workers, cfg.QueueSize),
		meters:            &meters{},
		settings:          newReader(nil, logger),
		logger:            logger,
		seen:              newSeenEvents(),
		sweepEvery:        sweepInterval,
		stopBound:         stopWait,
	}
}

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
