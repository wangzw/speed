// Package invite is the invitation module of the authentication capability: it
// owns the authn_invitations table, the invitation state machine and its routes.
package invite

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
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry; a host names it in the
// command that mounts the invitation routes.
const ModuleName = "authn.invite"

// ConfigNamespace is where this module's input items and settings are mounted.
const ConfigNamespace = ModuleName

const defaultEndpoint = "account"

//go:embed openapi/invite.json
var inviteFragment []byte

//go:embed openapi/invite.tenants.json
var inviteTenantsFragment []byte

var (
	inviteSpec = speedhttp.Spec{
		Endpoint: defaultEndpoint,
		Prefix:   "api",
		Version:  "v1",
		Document: inviteFragment,
	}
	inviteTenantsSpec = speedhttp.Spec{
		Endpoint: "api",
		Prefix:   "api",
		Version:  "v1",
		Scope:    speedhttp.ScopeTenants,
		Document: inviteTenantsFragment,
	}
)

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// Unreachable: the embed directive fails the build first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

var _ Invites = (*product)(nil)

var _ authn.InvitationRevoker = (*product)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. Every required capability is
// delivered exclusively by another module, so the host writes them down.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.TenantDescriptionResolver)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*Invites)(nil), Exclusive: true},
			{Token: (*authn.InvitationRevoker)(nil), Exclusive: true},
		},
		Resources: []any{
			configSchema(),
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
			inviteSpec,
			inviteTenantsSpec,
			tenantOperations,
			publicOperations,
			feature.Spec{Name: "authn.invitations"},
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

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			p.diagnose()
			return p.checkMigrated(ctx)
		},

		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	handle *gorm.DB
	// now is the clock every database time is read from, in UTC.
	now clock.Source

	accounts     authn.Accounts
	verification authn.SignupVerification
	memberships  authn.Memberships
	tenantDescs  authn.TenantDescriptionResolver

	mounter mount.Mounter
	audit   audit.Audit

	kv kv.KV

	store            settings.Store
	bus              event.Bus
	decider          authn.Decider
	notifier         authn.InviteNotifier
	tenantNames      authn.TenantNameResolver
	tenantState      authn.TenantStateResolver
	rolePolicy       authn.InviteRolePolicy
	membershipWriter membershipWriterTx
	roleBinder       provisioning.RoleBinder
	logger           *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newProduct(reg *core.Registry) (*product, error) {
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the route mounter: %w", ModuleName, err)
	}
	counters, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the key-value store: %w", ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account reads: %w", ModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the signup verification reads: %w", ModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership writes: %w", ModuleName, err)
	}
	tenantDescs, err := core.Resolve[authn.TenantDescriptionResolver](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant description resolver: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	return &product{
		handle:       database.DB(),
		accounts:     accounts,
		verification: verification,
		memberships:  memberships,
		tenantDescs:  tenantDescs,
		mounter:      mounter,
		audit:        auditor,
		kv:           counters,
		logger:       log.Default(),
		reported:     make(map[string]string),
	}, nil
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
		return zero, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	var err error
	if p.decider, err = optional[authn.Decider](reg, "the decision seam"); err != nil {
		return err
	}
	if p.store, err = optional[settings.Store](reg, "the settings store"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if p.notifier, err = optional[authn.InviteNotifier](reg, "the invitation notifier"); err != nil {
		return err
	}
	if p.tenantNames, err = optional[authn.TenantNameResolver](reg, "the tenant name resolver"); err != nil {
		return err
	}
	if p.tenantState, err = optional[authn.TenantStateResolver](reg, "the tenant state resolver"); err != nil {
		return err
	}
	if p.rolePolicy, err = optional[authn.InviteRolePolicy](reg, "the invitation role policy"); err != nil {
		return err
	}
	if writer, writerErr := optional[provisioning.MembershipWriter](reg, "the transaction-aware membership writer"); writerErr != nil {
		return writerErr
	} else if writer != nil {
		p.membershipWriter = writer
	}
	if p.roleBinder, err = optional[provisioning.RoleBinder](reg, "the transaction-aware role binder"); err != nil {
		return err
	}
	if p.bus != nil && p.store != nil {
		p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
	}
	if p.bus != nil {
		p.bus.Subscribe(authn.EventMembershipChanged, ModuleName, p.onMembershipChanged)
	}
	p.mountRoutes()
	return nil
}

func (p *product) conn(ctx context.Context) *gorm.DB { return p.handle.WithContext(ctx) }

func (p *product) at() time.Time { return utc(p.now.Now()) }

func (p *product) checkMigrated(ctx context.Context) error {
	if err := p.conn(ctx).Exec("SELECT 1 FROM " + tableInvitations + " LIMIT 1").Error; err != nil {
		return fmt.Errorf("%w: %s: the invitation table does not exist", authn.ErrNotMigrated, tableInvitations)
	}
	return nil
}

func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the invitation routes are mounted",
		"endpoint", p.mounter.Endpoint(), "base_path", p.mounter.BaseIn(speedhttp.Scope("")))
}
