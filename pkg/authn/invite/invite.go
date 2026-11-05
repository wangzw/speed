// Package invite is the invitation module of the authentication capability: it
// holds the authn_invitations table, the state machine of an invitation, and
// the routes of its issuance face and its acceptance face.
//
// An invitation is a pending grant of a membership: it names an address and a
// tenant and, until it is accepted, no account and no membership row exists.
// One module owns the whole of it — the table, the token, and both faces of the
// routes — and delivers the Invites capability exclusively. It does not keep
// accounts, memberships or passwords: acceptance creates an account through
// AccountAdmin and writes the membership through Memberships, with invite as the
// membership's source, and those rules stay with the account module.
//
// The issuance face, under the base path's /admin tree, is decided in the
// handler by the Decider seam, with the action authn.membership.manage, and a
// write lands its mandatory audit row before the action. The acceptance face,
// under the base path's /invitations, is public: the token is the whole
// credential, and there is no identity to read.
//
// The module renders no message and runs no retry loop. After a successful
// management issue it asks the optional InviteNotifier seam, bounded to five
// seconds, and reports the outcome beside the one-time link: a missing notifier
// is not_configured, and a failed notice is logged and audited best-effort
// without undoing the invitation.
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
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the invitation routes.
const ModuleName = "authn.invite"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

// defaultEndpoint is the endpoint this module's ordinary OpenAPI fragment
// declares its routes for. The endpoint the routes are actually mounted on is
// the one the mounter resolves; the fragment is a static declaration written
// before that is known, so it carries the conventional name. The
// tenant-management fragment names api, the conventional endpoint for routes
// decided in a tenant's policy domain.
const defaultEndpoint = "account"

// inviteFragment is this module's public OpenAPI fragment, describing the
// acceptance routes. It is generated from the operation declarations, never
// written by hand.
//
//go:embed openapi/invite.json
var inviteFragment []byte

// inviteTenantsFragment is this module's tenant-domain fragment, describing the
// management routes.
//
//go:embed openapi/invite.tenants.json
var inviteTenantsFragment []byte

// inviteSpec and inviteTenantsSpec are the two APIs this module's declarations
// hang off: the ordinary acceptance surface and the tenant management one. Their
// prefix and version are the common ones; the fragments write the capability's
// own segment, so a route's declared path begins with the part of the mounter's
// base that lies below the root.
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

// migrationsFS is migrationRoot with the wrapping directory removed, so the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// product must satisfy the capability it delivers.
var _ Invites = (*product)(nil)

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the invitation
// routes" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The database, the route mounter, the account read and write surfaces, the
// signup verification reads, the membership writes and the audit capability are
// required, and each is
// delivered exclusively by another module, so the closure of a command that
// takes this one in does not pull them in: the host writes them down. The audit
// capability being required is what makes an assembly without one fail before
// any instance is built, rather than at the first write. The decision seam, the
// settings store, the event bus, the invitation notifier and the logger are
// taken up in Init and none is required: the absence of a Decider is a legal
// assembly in which every management request is refused, without the settings
// the lifetime reads as its declared default, and without a notifier the issue
// answer reports the notice as not_configured.
//
// The stance is StateEnabled and never StateAuto: whether to mount the
// invitation routes is a decision on the command tree, not a conclusion a
// configuration can infer.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*Invites)(nil), Exclusive: true}},
		Resources: []any{
			configSchema(),
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
			inviteSpec,
			inviteTenantsSpec,
			tenantOperations,
			publicOperations,
			// The invitation faces: issued and accepted on routes mounted
			// whenever the module is in force, so the feature is static.
			feature.Spec{Name: "authn.invitations"},
		},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			// The module has no configuration items of its own; the lifetime it
			// issues with is a setting, read when an invitation is issued.
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

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for this module, and the callbacks
// leave it alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It holds the required capabilities and the
// optional ones taken up in Init.
type product struct {
	handle *gorm.DB
	// now is the clock every time written to the database or bound to a query
	// is read from, in UTC.
	now clock.Source

	accounts     authn.Accounts
	accountAdm   authn.AccountAdmin
	verification authn.SignupVerification
	memberships  authn.Memberships

	mounter mount.Mounter
	audit   audit.Audit

	// The optional capabilities. Each is nil when absent: a missing Decider
	// refuses every management request, without the settings the lifetime reads
	// as its declared default, and without a notifier the issuance answer
	// reports the notice as not_configured.
	store    settings.Store
	bus      event.Bus
	decider  authn.Decider
	notifier authn.InviteNotifier
	logger   *slog.Logger

	// mu guards reported, which holds for each fault the one last written, so
	// that a setting read on every issuance is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

// newProduct takes up what the descriptor requires.
func newProduct(reg *core.Registry) (*product, error) {
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the route mounter: %w", ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account reads: %w", ModuleName, err)
	}
	accountAdm, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account writes: %w", ModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the signup verification reads: %w", ModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership writes: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	return &product{
		handle:       database.DB(),
		accounts:     accounts,
		accountAdm:   accountAdm,
		verification: verification,
		memberships:  memberships,
		mounter:      mounter,
		audit:        auditor,
		logger:       log.Default(),
		reported:     make(map[string]string),
	}, nil
}

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the start,
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
		return zero, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

// wire runs in Init, where every instance exists. It takes up the optional
// capabilities and mounts the routes.
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
	if p.bus != nil && p.store != nil {
		p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
	}
	p.mountRoutes()
	return nil
}

// conn is the database handle bound to the context of one call.
func (p *product) conn(ctx context.Context) *gorm.DB { return p.handle.WithContext(ctx) }

// at is the current time as the database stores it.
func (p *product) at() time.Time { return utc(p.now.Now()) }

// checkMigrated reports that the module's table is not there, so that a command
// which forgot to apply the migrations fails at the start rather than at the
// first request. A table that cannot be read at all is the database not
// answering.
func (p *product) checkMigrated(ctx context.Context) error {
	if err := p.conn(ctx).Exec("SELECT 1 FROM " + tableInvitations + " LIMIT 1").Error; err != nil {
		return fmt.Errorf("%w: %s: the invitation table does not exist", authn.ErrNotMigrated, tableInvitations)
	}
	return nil
}

// diagnose writes the start diagnostic: the endpoint the invitation routes were
// mounted on.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the invitation routes are mounted",
		"endpoint", p.mounter.Endpoint(), "base_path", p.mounter.BaseIn(speedhttp.Scope("")))
}
