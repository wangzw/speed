// Package policy is the authorization policy module: the policy tables and
// their migrations, the thin store the engine reads and writes through, the
// permission catalog, the policy snapshot the enforcement path reads, the
// management surface over the policy data, and the account lifecycle cleanup.
//
// It delivers the enforcement engine, the subject-permission read, the catalog
// and the manager, each exclusively, and implements the authentication
// capability's account lifecycle hook, membership lifecycle hook and membership
// guard. The Casbin engine and the database it reads live here and nowhere else,
// so a host that only guards routes carries no policy store.
package policy

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/policy"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. It is also the namespace
// this module's input items are mounted under, which is what a registering
// module's namespace always is.
const ModuleName = "authz.policy"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
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
// It delivers the engine, the subject-permission read, the catalog, the manager
// and the seeding surface, each exclusively: two engines would be two snapshots
// a decision could land in with nothing to choose between them, two
// subject-permission reads two answers to what one subject holds, two catalogs
// two answers to whether a permission is declared, and two managers or two
// seeders two writers of one table. A second provider of any of them fails the
// startup.
//
// The database is the one hard dependency: the policy lives in its own tables
// and there is no engine without it. The roster, the membership checker, the
// tenant lock, the bus and the logger are taken up in Init and each is optional
// — a policy module with no roster has no platform-domain tenant to check, one
// with no membership checker refuses a platform administrator binding rather
// than writing it, one with no tenant lock serialises its removals in-process,
// one with no bus relies on the configured refresh interval, and one with no
// logger writes through the process default. The module implements the
// authentication capability's account lifecycle, membership lifecycle and
// membership guard on the same instance, so an assembly that takes this module
// in has all three without naming them apart.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authz.Enforcer)(nil), Exclusive: true},
			{Token: (*authz.SubjectPermissions)(nil), Exclusive: true},
			{Token: (*authz.Catalog)(nil), Exclusive: true},
			{Token: (*authz.Manager)(nil), Exclusive: true},
			{Token: (*authz.Seeder)(nil), Exclusive: true},
			{Token: (*authn.AccountLifecycle)(nil)},
			{Token: (*authn.MembershipLifecycle)(nil)},
			{Token: (*authn.MembershipGuard)(nil)},
		},
		Resources: []any{
			permissions,
			db.Migrations{FS: migrationsFS},
			schema(),
			messageCatalog,
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
				handle:          database.DB(),
				dialect:         database.Dialect(),
				text:            authz.ModelText,
				loadTimeout:     cfg.loadTimeout,
				refreshInterval: cfg.refreshInterval,
				catalog:         buildCatalog(reg),
				logger:          log.Default(),
				tenantLock:      authz.NewInProcessTenantLock(),
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
			// The process's translation is given back with the assembly that
			// resolved it: Stop runs at the end of every drive, the one that
			// arrived and the one that failed on the way, so a later assembly
			// sharing the process resolves its own set rather than reading this
			// one's. An entry point has already drained by this point — Stop
			// reaches the entry points first — so no request is deciding on the
			// set being dropped.
			translation.Reset()
			return nil
		},
	}
}

var (
	_ authz.Enforcer            = (*product)(nil)
	_ authz.SubjectPermissions  = (*product)(nil)
	_ authz.Catalog             = (*product)(nil)
	_ authz.Manager             = (*product)(nil)
	_ authz.Seeder              = (*product)(nil)
	_ authn.AccountLifecycle    = (*product)(nil)
	_ authn.MembershipLifecycle = (*product)(nil)
	_ authn.MembershipGuard     = (*product)(nil)
)

// product is this module's product and the implementation of the enforcement
// engine, the catalog, the manager, the seeding surface and the account
// lifecycle hook.
type product struct {
	handle      *gorm.DB
	dialect     db.Dialect
	text        string
	loadTimeout time.Duration
	// refreshInterval sizes the ticker Start raises. Zero turns the periodic
	// reload off.
	refreshInterval time.Duration

	catalog *catalog
	logger  *slog.Logger

	// The rest is settled in wire, before any request is served.
	bus        event.Bus
	roster     tenant.Catalog
	membership authz.MembershipChecker

	holder policy.Holder
	// tenantLock serialises the writes that judge a tenant's
	// tenant-administrator floor: this product's binding deletes and every
	// membership guard's removal lock. The host supplies authz.TenantLock; with
	// none, the in-process default serialises this process only. See lock.go and
	// authz.TenantLock.
	tenantLock authz.TenantLock
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

// wire resolves the process's permission translation and takes up the optional
// capabilities, and subscribes to the change event.
//
// The translation is resolved first, because it is the vocabulary the policy
// data is written in and a disagreement in it is a startup failure the fix for
// which is visible before anything else runs. This module owns the resolution
// rather than the enforcement module reading decisions off it: the catalog and
// the translation are built over the same registered modules and are one set of
// data, and this module is the one every command that writes policy data already
// takes in — so a bootstrap that seeds roles reaches the translation without the
// enforcement module and without the HTTP entry point that module requires.
func (p *product) wire(reg *core.Registry) error {
	if err := translation.Resolve(reg); err != nil {
		return err
	}
	// The reverse map is built from the process's translation, so it is
	// rebuilt once that set holds every module's declared rows.
	p.catalog.indexReverse()
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
	if checker, found, err := optional[authz.MembershipChecker](reg, "the membership checker"); err != nil {
		return err
	} else if found {
		p.membership = checker
	}
	if lock, found, err := optional[authz.TenantLock](reg, "the tenant lock"); err != nil {
		return err
	} else if found {
		p.tenantLock = lock
	}
	if bus, found, err := optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	} else if found {
		p.bus = bus
		// The subscriptions belong to Init: a bus freezes its subscription table
		// when it starts, so a subscriber that arrived later would miss every
		// event published before it. The membership subscription is what
		// withdraws the platform-domain bindings of an account that has left the
		// platform tenant.
		bus.Subscribe(policyChangedEventType, ModuleName, p.onChanged)
		bus.Subscribe(authn.EventMembershipChanged, ModuleName, p.onMembershipChanged)
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

// Held implements authz.SubjectPermissions. It reads the current snapshot the
// same way Allowed does, maps each held (object, action) pair back through the
// catalog, and answers the permission names once each, sorted. A pair no
// declared permission translates to is left out; a snapshot that has not been
// loaded and an engine failure are both unavailable rather than an empty set.
func (p *product) Held(ctx context.Context, domain authz.Domain, subject string) ([]authz.Permission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot := p.holder.Get()
	if snapshot == nil {
		return nil, fmt.Errorf("%w: the policy snapshot has not been loaded", authz.ErrPolicyUnavailable)
	}
	pairs, err := snapshot.Held(domain, subject)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the subject's permissions: %w", authz.ErrPolicyUnavailable, err)
	}
	held := make([]authz.Permission, 0, len(pairs))
	seen := make(map[authz.Permission]struct{}, len(pairs))
	for _, pair := range pairs {
		perm, ok := p.catalog.permissionOf(pair[0], pair[1])
		if !ok {
			continue
		}
		if _, duplicate := seen[perm]; duplicate {
			continue
		}
		seen[perm] = struct{}{}
		held = append(held, perm)
	}
	slices.Sort(held)
	return held, nil
}

// Declared implements authz.Catalog.
func (p *product) Declared() []authz.Permission { return p.catalog.Declared() }

// Has implements authz.Catalog.
func (p *product) Has(perm authz.Permission) bool { return p.catalog.Has(perm) }

// ObjectAction answers the (object, action) pair a permission translates to.
func (p *product) ObjectAction(perm authz.Permission) (object, action string, ok bool) {
	return p.catalog.ObjectAction(perm)
}

// DescriptionKey implements authz.Catalog.
func (p *product) DescriptionKey(perm authz.Permission) (key string, ok bool) {
	return p.catalog.DescriptionKey(perm)
}

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
// zero.
func (p *product) startRefresh(ctx context.Context) {
	if p.refreshInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	p.cancelRefresh, p.refreshDone = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(p.refreshInterval)
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
