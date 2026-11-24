// Package account stores the accounts of the authentication capability, their
// identifiers, external identities, tenant memberships, password credentials,
// service secrets and the failure back-off.
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
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
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

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

var (
	_ authn.Accounts            = (*product)(nil)
	_ authn.AccountAdmin        = (*product)(nil)
	_ authn.Signups             = (*product)(nil)
	_ authn.SignupVerification  = (*product)(nil)
	_ authn.Passwords           = (*product)(nil)
	_ authn.Memberships         = (*product)(nil)
	_ authn.MembershipCleanup   = (*product)(nil)
	_ authn.Throttle            = (*product)(nil)
	_ authn.UsernameReservation = (*product)(nil)
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. The database and the provisioning
// seam are required and the delivered capabilities exclusive; the remaining
// stores, hooks, metrics and logger are taken up in Init when present.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*provisioning.AccountProvisioning)(nil)},
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
			{Token: (*authn.UsernameReservation)(nil), Exclusive: true},
			{Token: (*provisioning.MembershipWriter)(nil), Exclusive: true},
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

		// New reads none of the module's tables: it runs before migrations.
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

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	handle  *gorm.DB
	dialect db.Dialect
	cfg     resolved

	gov        *password.Governor
	decoy      password.Decoy
	signupGov  *password.Governor
	signupWait time.Duration
	wait       time.Duration
	// now must be the source of every time written to the database or bound to a
	// query, in UTC.
	now                                    clock.Source
	stopWait                               time.Duration
	sweepThrottlesEvery, sweepSecretsEvery time.Duration

	store              settings.Store
	bus                event.Bus
	outbox             outbox.Outbox
	audit              audit.Audit
	hooks              []authn.AccountLifecycle
	membershipHooks    []authn.MembershipLifecycle
	provisioning       provisioning.AccountProvisioning
	membershipGuards   []authn.MembershipGuard
	tenantAdminCounter authz.TenantAdminCounter
	tenantOwnerCounter authz.TenantOwnerCounter
	meters             meters
	logger             *slog.Logger

	mu       sync.Mutex
	reported map[string]string

	sweepers sweepers
}

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
		provisioning:        noopProvisioning{},
		reported:            make(map[string]string),
	}
}

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
	if p.tenantAdminCounter, err = optional[authz.TenantAdminCounter](reg, "the tenant-admin counter"); err != nil {
		return err
	}
	if p.tenantOwnerCounter, err = optional[authz.TenantOwnerCounter](reg, "the tenant-owner counter"); err != nil {
		return err
	}
	if p.provisioning, err = core.Resolve[provisioning.AccountProvisioning](reg); err != nil {
		return fmt.Errorf("%s: taking up the account provisioning seam: %w", ModuleName, err)
	}

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

func (p *product) conn(ctx context.Context) *gorm.DB { return p.handle.WithContext(ctx) }

func utc(t time.Time) time.Time { return clock.UTC(t).Truncate(time.Microsecond) }

func (p *product) at() time.Time { return utc(p.now.Now()) }

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := utc(t)
	return &u
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return clock.UTC(*t)
}
