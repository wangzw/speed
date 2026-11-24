// Package policy is the authorization policy module: the policy tables and
// their migrations, the store the engine reads and writes through, the
// permission catalog, the policy snapshot the enforcement path reads, the
// management surface over the policy data, and the account lifecycle cleanup.
//
// The Casbin engine and the database it reads live here and nowhere else.
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
	"github.com/vislake/speed/pkg/authn/provisioning"
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

// ModuleName is this module's name in the registry.
const ModuleName = "authz.policy"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// policyChangedEventType is the type name of the event a write publishes so
// that the other replicas reload their snapshots.
const policyChangedEventType = "authz.policy.changed"

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers the engine, the subject-permission read, the catalog, the manager,
// the seeding surface and the converging surface, each exclusively.
//
// The database is the one hard dependency. The roster, the membership checker,
// the tenant lock, the bus and the logger are taken up in Init and each is
// optional.
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
			{Token: (*authz.Reconciler)(nil), Exclusive: true},
			{Token: (*authz.TenantAdminCounter)(nil), Exclusive: true},
			{Token: (*authz.TenantOwnerCounter)(nil), Exclusive: true},
			{Token: (*TxManager)(nil), Exclusive: true},
			{Token: (*authn.AccountLifecycle)(nil)},
			{Token: (*authn.MembershipLifecycle)(nil)},
			{Token: (*authn.MembershipGuard)(nil)},
			{Token: (*authn.MemberRoleReader)(nil)},
			{Token: (*authn.InviteRolePolicy)(nil)},
			{Token: (*provisioning.RoleBinder)(nil)},
		},
		Resources: []any{
			permissions,
			db.Migrations{FS: migrationsFS},
			schema(),
			messageCatalog,
			roleMessageCatalog,
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
			catalog, err := buildCatalog(reg)
			if err != nil {
				return nil, fmt.Errorf("%s: building the permission catalog: %w", ModuleName, err)
			}
			return &product{
				handle:          database.DB(),
				dialect:         database.Dialect(),
				text:            authz.ModelText,
				loadTimeout:     cfg.loadTimeout,
				refreshInterval: cfg.refreshInterval,
				catalog:         catalog,
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
			// Stop runs at the end of every drive, so a later assembly sharing the
			// process resolves its own translation set rather than reading this
			// one's.
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
	_ authz.Reconciler          = (*product)(nil)
	_ authz.TenantAdminCounter  = (*product)(nil)
	_ authz.TenantOwnerCounter  = (*product)(nil)
	_ authn.AccountLifecycle    = (*product)(nil)
	_ authn.MembershipLifecycle = (*product)(nil)
	_ authn.MembershipGuard     = (*product)(nil)
	_ authn.MemberRoleReader    = (*product)(nil)
	_ authn.InviteRolePolicy    = (*product)(nil)
	_ provisioning.RoleBinder   = (*product)(nil)
)

// product is this module's product and the implementation of the enforcement
// engine, the catalog, the manager, the seeding surface, the converging surface
// and the account lifecycle hook.
type product struct {
	handle          *gorm.DB
	dialect         db.Dialect
	text            string
	loadTimeout     time.Duration
	refreshInterval time.Duration

	catalog *catalog
	logger  *slog.Logger

	// The rest is settled in wire, before any request is served.
	bus        event.Bus
	roster     tenant.Catalog
	membership authz.MembershipChecker
	// membershipReads lets the account-administration lock also cover the active
	// business tenants the account belongs to without administering: an ownership
	// transfer makes an active member the owner.
	membershipReads authn.Memberships
	// writeGuard tells the tenant-administrator counter which domains are not
	// business tenants.
	writeGuard authz.WriteGuard

	holder policy.Holder
	// tenantLock serialises the writes that judge a tenant's
	// tenant-administrator floor.
	tenantLock authz.TenantLock
	// reloadMu serialises the reload path, so two concurrent loads cannot
	// install their results out of order.
	reloadMu sync.Mutex

	cancelRefresh context.CancelFunc
	refreshDone   chan struct{}
}

// productOf narrows an instance to this module's product. An instance of
// another shape is left alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok
}

// wire resolves the process's permission translation, takes up the optional
// capabilities, and subscribes to the change event. The translation is resolved
// first because it is the vocabulary the policy data is written in.
func (p *product) wire(reg *core.Registry) error {
	if err := translation.Resolve(reg); err != nil {
		return err
	}
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
	if memberships, found, err := optional[authn.Memberships](reg, "the membership read surface"); err != nil {
		return err
	} else if found {
		p.membershipReads = memberships
	}
	if guard, found, err := optional[authz.WriteGuard](reg, "the write guard"); err != nil {
		return err
	} else if found {
		p.writeGuard = guard
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
		// when it starts, so a subscriber that arrived later would miss earlier
		// events.
		bus.Subscribe(policyChangedEventType, ModuleName, p.onChanged)
		bus.Subscribe(authn.EventMembershipChanged, ModuleName, p.onMembershipChanged)
	}
	return nil
}

// optional takes up a capability that may be absent. Absence is legal and leaves
// the zero value; any other failure stops the startup.
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

// begin runs the startup checks and loads the first snapshot. The
// platform-domain check runs first, the load is bounded by the configured
// timeout, and the model self-check runs before the refresh starts.
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

// Allowed implements authz.Enforcer. A snapshot that has not been loaded is
// unavailable rather than a denial.
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

// Held implements authz.SubjectPermissions. It maps each held (object, action)
// pair back through the catalog and answers the permission names once each,
// sorted. A snapshot that has not been loaded and an engine failure are both
// unavailable rather than an empty set.
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
		perm, ok := p.catalog.PermissionOf(pair[0], pair[1])
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

// TenantGoverned implements authz.Catalog.
func (p *product) TenantGoverned() []authz.Permission { return p.catalog.TenantGoverned() }

// Governed implements authz.Catalog.
func (p *product) Governed(perm authz.Permission) bool { return p.catalog.Governed(perm) }

// Has implements authz.Catalog.
func (p *product) Has(perm authz.Permission) bool { return p.catalog.Has(perm) }

// ObjectAction answers the (object, action) pair a permission translates to.
func (p *product) ObjectAction(perm authz.Permission) (object, action string, ok bool) {
	return p.catalog.ObjectAction(perm)
}

// PermissionOf answers the permission a (object, action) pair translates back
// to, and whether any declared permission translates to it.
func (p *product) PermissionOf(object, action string) (authz.Permission, bool) {
	return p.catalog.PermissionOf(object, action)
}

// DescriptionKey implements authz.Catalog.
func (p *product) DescriptionKey(perm authz.Permission) (key string, ok bool) {
	return p.catalog.DescriptionKey(perm)
}

// Metadata implements authz.Catalog.
func (p *product) Metadata(perm authz.Permission) (authz.PermissionMeta, bool) {
	return p.catalog.Metadata(perm)
}

// swap reads the whole policy and installs it as the current snapshot.
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

// reload swaps the snapshot, keeping the one in place when the load fails. A
// cancellation is recorded at Debug; every other failure at Error.
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

// onChanged reloads on a change event published by another replica.
func (p *product) onChanged(ctx context.Context, _ event.Event) error {
	p.reload(ctx)
	return nil
}

// publish sends the change event after a write. A failure is recorded and goes
// no further: the row is already in the database.
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

// stopRefresh cancels the ticker and waits for its goroutine to return, so a
// reload in flight does not outlive Stop.
func (p *product) stopRefresh() {
	if p.cancelRefresh == nil {
		return
	}
	p.cancelRefresh()
	<-p.refreshDone
	p.cancelRefresh, p.refreshDone = nil, nil
}
