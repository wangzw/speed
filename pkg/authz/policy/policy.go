// Package policy is the authorization policy module: the policy tables and
// their migrations, the thin store the engine reads and writes through, the
// permission catalog, the policy snapshot the enforcement path reads, the
// management surface over the policy data, and the account lifecycle cleanup.
//
// It delivers the enforcement engine, the catalog and the manager, each
// exclusively, and implements the authentication capability's account lifecycle
// hook. The Casbin engine and the database it reads live here and nowhere else,
// so a host that only guards routes carries no policy store.
package policy

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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. It is also the namespace
// this module's input items and settings are mounted under, which is what a
// registering module's namespace always is.
const ModuleName = "authz.policy"

// ConfigNamespace is where this module's input items are mounted, and the
// prefix of its settings. It is this module's own name, as a registering
// module's namespace always is.
const ConfigNamespace = ModuleName

// policyChangedEventType is the type name of the event a write publishes so
// that the other replicas reload their snapshots. It is agreed between
// publisher and subscriber with nothing checking the two ends against each
// other.
const policyChangedEventType = "authz.policy.changed"

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// The module registers itself. Importing this package is what puts the policy
// store in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers the engine, the catalog and the manager, each exclusively: two
// engines would be two snapshots a decision could land in with nothing to
// choose between them, two catalogs two answers to whether a permission is
// declared, and two managers two writers of one table. A second provider of any
// of them fails the startup.
//
// The database is the one hard dependency: the policy lives in its own tables
// and there is no engine without it. The roster, the bus, the settings store and
// the logger are taken up in Init and each is optional — a policy module with no
// roster has no platform-domain conflict to check, one with no bus relies on the
// refresh interval, one with no settings carries no refresh, and one with no
// logger writes through the process default.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authz.Enforcer)(nil), Exclusive: true},
			{Token: (*authz.Catalog)(nil), Exclusive: true},
			{Token: (*authz.Manager)(nil), Exclusive: true},
			{Token: (*authn.AccountLifecycle)(nil)},
		},
		Resources: []any{
			permissions,
			db.Migrations{FS: migrationsFS},
			schema(),
			settingsSchema(),
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w",
					ModuleName, err)
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
			}
			return &product{
				handle:      database.DB(),
				text:        authz.ModelText,
				loadTimeout: cfg.loadTimeout,
				catalog:     buildCatalog(reg),
				logger:      log.Default(),
			}, nil
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
			return p.begin(ctx)
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.stopRefresh()
			}
			return nil
		},
	}
}

var (
	_ authz.Enforcer         = (*product)(nil)
	_ authz.Catalog          = (*product)(nil)
	_ authz.Manager          = (*product)(nil)
	_ authn.AccountLifecycle = (*product)(nil)
)

// product is this module's product and the implementation of the enforcement
// engine, the catalog, the manager and the account lifecycle hook.
type product struct {
	handle      *gorm.DB
	text        string
	loadTimeout time.Duration

	catalog *catalog
	logger  *slog.Logger

	// The rest is settled in wire, before any request is served.
	bus    event.Bus
	store  settings.Store
	roster tenant.Catalog

	holder policy.Holder
	// reloadMu serialises the reload path, a read of the table followed by a
	// swap, so that two concurrent loads cannot install their results out of
	// order and leave a staler snapshot in place.
	reloadMu sync.Mutex

	// cancelRefresh stops the ticker and refreshDone is closed once its
	// goroutine has returned. Both are written in Start and read in Stop, which
	// the lifecycle driver runs one after the other on its own goroutine.
	cancelRefresh context.CancelFunc
	refreshDone   chan struct{}
}

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for the module, and the callbacks
// leave it alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok
}

// wire takes up the optional capabilities and subscribes to the change event.
func (p *product) wire(reg *core.Registry) error {
	if delivered, found, err := optional[log.Logger](reg, "the logger"); err != nil {
		return err
	} else if found {
		p.logger = delivered.Named(ModuleName)
	}
	if roster, found, err := optional[tenant.Catalog](reg, "the tenant roster"); err != nil {
		return err
	} else if found {
		p.roster = roster
	}
	if store, found, err := optional[settings.Store](reg, "the settings"); err != nil {
		return err
	} else if found {
		p.store = store
	}
	if bus, found, err := optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	} else if found {
		p.bus = bus
		// The subscription belongs to Init: a bus freezes its subscription table
		// when it starts, so a subscriber that arrived later would miss every
		// event published before it.
		bus.Subscribe(policyChangedEventType, ModuleName, p.onChanged)
	}
	return nil
}

// optional takes up a capability that may be absent. Absence is the legal
// configuration and leaves the zero value; any other failure is carried out and
// stops the startup.
func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		var zero T
		return zero, false, nil
	}
	var zero T
	return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
}

// begin runs the startup checks and loads the first snapshot.
//
// The platform-domain check runs first, so a roster that cannot be honoured
// fails the startup before anything is read. The load is bounded by the
// configured timeout; a load that does not finish fails the startup rather than
// serving from a half-read snapshot. The model self-check runs on the loaded
// text last, before the refresh starts: a model that behaves other than the
// design states must not begin serving.
func (p *product) begin(ctx context.Context) error {
	if p.roster != nil {
		if err := checkPlatformDomain(ctx, p.roster); err != nil {
			return err
		}
	}

	loadCtx, cancel := context.WithTimeout(ctx, p.loadTimeout)
	err := p.swap(loadCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("%w: loading the policy: %w", authz.ErrPolicyUnavailable, err)
	}

	if err := policy.SelfCheck(p.text); err != nil {
		return fmt.Errorf("%w: %w", authz.ErrPolicyUnavailable, err)
	}

	p.startRefresh(ctx)
	return nil
}

// Allowed implements authz.Enforcer. It reads the current snapshot and runs the
// matcher; a snapshot that has not been loaded is unavailable rather than a
// denial, because the process cannot yet answer from a policy it read.
func (p *product) Allowed(ctx context.Context, q authz.Query) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	snapshot := p.holder.Get()
	if snapshot == nil {
		return false, fmt.Errorf("%w: the policy snapshot has not been loaded", authz.ErrPolicyUnavailable)
	}
	return snapshot.Allowed(q)
}

// Declared implements authz.Catalog.
func (p *product) Declared() []authz.Permission { return p.catalog.Declared() }

// Has implements authz.Catalog.
func (p *product) Has(perm authz.Permission) bool { return p.catalog.Has(perm) }

// swap reads the whole policy and installs it as the current snapshot. It is
// the same operation for every trigger — the startup load, a write, a delivered
// event, the refresh ticker — because the snapshot is replaced as a whole and
// the replacement is idempotent.
func (p *product) swap(ctx context.Context) error {
	p.reloadMu.Lock()
	defer p.reloadMu.Unlock()
	snapshot, err := policy.Load(ctx, p.handle, p.text)
	if err != nil {
		return err
	}
	p.holder.Swap(snapshot)
	return nil
}

// reload swaps the snapshot, keeping the one in place when the load fails.
//
// A reload whose context was cancelled was withdrawn by whoever triggered it,
// and its failure is that cancellation rather than anything about the table, so
// it is recorded at Debug; every other failure is recorded at Error and leaves
// the previous snapshot serving.
func (p *product) reload(ctx context.Context) {
	if err := p.swap(ctx); err != nil {
		if ctx.Err() != nil {
			p.logger.Debug("authz.policy: a reload was withdrawn before it finished; the "+
				"previous snapshot stays in use", "error", err)
			return
		}
		p.logger.Error("authz.policy: reloading the snapshot failed; the previous one stays in use",
			"error", err)
	}
}

// onChanged reloads on a change event published by another replica. The
// handler does one thing and returns, wherever the bus calls it.
func (p *product) onChanged(ctx context.Context, _ event.Event) error {
	p.reload(ctx)
	return nil
}

// publish sends the change event after a write.
//
// A failure here is recorded and goes no further: the row is in the database,
// and reporting the write as failed because its notification could not be sent
// would tell the caller the opposite of what happened.
func (p *product) publish(ctx context.Context) {
	if p.bus == nil {
		return
	}
	if err := p.bus.Publish(ctx, event.Event{Type: policyChangedEventType}); err != nil {
		p.logger.Error("authz.policy: publishing the change event failed; other replicas reload "+
			"at their next refresh or a later change", "error", err)
	}
}

// startRefresh begins the periodic reload, unless the configured interval is
// zero or no settings store is present.
func (p *product) startRefresh(ctx context.Context) {
	interval := p.refreshInterval()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	p.cancelRefresh, p.refreshDone = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.reload(ctx)
			}
		}
	}()
}

// stopRefresh cancels the ticker and waits for its goroutine to return, so that
// a reload in flight does not outlive Stop and meet the database handle being
// closed under it.
func (p *product) stopRefresh() {
	if p.cancelRefresh == nil {
		return
	}
	p.cancelRefresh()
	<-p.refreshDone
	p.cancelRefresh, p.refreshDone = nil, nil
}

// refreshInterval reads the reload interval from the settings store. An
// absent store, an unreadable value and a read failure all mean no periodic
// reload: the design's default for the item is zero, which is off.
func (p *product) refreshInterval() time.Duration {
	if p.store == nil {
		return 0
	}
	interval, err := settings.Get[time.Duration](p.store, settings.Global(), refreshIntervalKey)
	if err != nil {
		p.logger.Warn("authz.policy: reading the refresh interval failed; the periodic reload "+
			"stays off", "error", err)
		return 0
	}
	return interval
}
