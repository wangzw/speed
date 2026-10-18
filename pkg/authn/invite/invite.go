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
// The module delivers no message and does not retry: an acceptance link is
// handed to the issuer once, and delivery is the issuer's or the host's.
package invite

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
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the invitation routes.
const ModuleName = "authn.invite"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

// defaultEndpoint is the endpoint this module's own OpenAPI fragment declares its
// routes for. The endpoint the routes are actually mounted on is the one the
// mounter resolves; the fragment is a static declaration written before that is
// known, so it carries the conventional name.
const defaultEndpoint = "account"

// inviteFragment is this module's OpenAPI fragment, describing the management
// and acceptance routes.
//
//go:embed openapi/invite.json
var inviteFragment []byte

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
// membership writes and the audit capability are required, and each is
// delivered exclusively by another module, so the closure of a command that
// takes this one in does not pull them in: the host writes them down. The audit
// capability being required is what makes an assembly without one fail before
// any instance is built, rather than at the first write. The decision seam, the
// throttle and the logger are taken up in Init: the absence of a Decider is a
// legal assembly in which every management request is refused.
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
			{Token: (*authn.Memberships)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*Invites)(nil), Exclusive: true}},
		Resources: []any{
			schema(),
			db.Migrations{FS: migrationsFS},
			speedhttp.Spec{Endpoint: defaultEndpoint, Prefix: "api", Version: "v1", Document: inviteFragment},
			// The invitation faces: issued and accepted on routes mounted
			// whenever the module is in force, so the feature is static.
			feature.Spec{Name: "authn.invitations"},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
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
			p.diagnose()
			return p.checkMigrated(ctx)
		},

		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// readerOf takes up the configuration reader. Its absence means the host never
// handed one to the registry, which is core's own failure and is carried out as
// it stands.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
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
	cfg    resolved
	// now is the clock every time written to the database or bound to a query
	// is read from, in UTC.
	now clock.Source

	accounts    authn.Accounts
	accountAdm  authn.AccountAdmin
	memberships authn.Memberships

	mounter mount.Mounter
	audit   audit.Audit

	// The optional capabilities. Each is nil when absent: a missing Decider
	// refuses every management request.
	decider authn.Decider
	logger  *slog.Logger
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
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership writes: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	return &product{
		handle:      database.DB(),
		cfg:         cfg,
		accounts:    accounts,
		accountAdm:  accountAdm,
		memberships: memberships,
		mounter:     mounter,
		audit:       auditor,
		logger:      log.Default(),
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
		"endpoint", p.mounter.Endpoint(), "base_path", p.mounter.Base())
}
