// Package ownership performs the organization writes that span the roster, the
// policy store and the membership store: an owner-transfer request and its
// confirmation, and an organization deletion.
//
// A submission does not change ownership. It records a pending request in this
// module's own tenant_owner_transfers table; only the target's confirmation
// writes the roster's owner column and the new owner's tenant-admin binding,
// and the previous owner keeps its membership and every binding it held. A
// deletion writes the roster tombstone, removes the tenant's memberships,
// bindings, roles and grants and cancels its pending request. The confirmation
// and the deletion must stand or fall as one transaction, so they live in a
// module that holds the transaction-aware surfaces of the stores involved
// rather than in any one of them.
//
// The module mounts no route and delivers no configurable behavior. The
// platform management surface, the organization's own tenant surface and the
// target's account-level surface take the capability up and call it; the module
// cannot tell them apart.
package ownership

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.ownership"

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

//go:embed locales/*.json
var localesFS embed.FS

// notificationTypes are the four kinds of message this module declares. The
// request notice goes to the target; the confirmed and declined results go to
// the account that made the request, and the expired type is declared for a
// later active-expiry notice but is not dispatched in this release. They are
// in-app only: a target need not have a proved email address, and the target
// page is the authoritative place to act either way.
var notificationTypes = []notification.TypeSpec{
	{
		Key:                    notificationTransferRequested,
		Group:                  "tenant",
		DefaultChannels:        []notification.Channel{notification.ChannelInApp},
		Unsubscribable:         false,
		RecipientVisibleParams: []string{"tenant", "request", "expires_at"},
	},
	{
		Key:                    notificationTransferConfirmed,
		Group:                  "tenant",
		DefaultChannels:        []notification.Channel{notification.ChannelInApp},
		Unsubscribable:         false,
		RecipientVisibleParams: []string{"tenant"},
	},
	{
		Key:                    notificationTransferDeclined,
		Group:                  "tenant",
		DefaultChannels:        []notification.Channel{notification.ChannelInApp},
		Unsubscribable:         false,
		RecipientVisibleParams: []string{"tenant"},
	},
	{
		Key:                    notificationTransferExpired,
		Group:                  "tenant",
		DefaultChannels:        []notification.Channel{notification.ChannelInApp},
		Unsubscribable:         false,
		RecipientVisibleParams: []string{"tenant"},
	},
}

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers
// tenant.OrganizationWriter and tenant.OwnershipTarget, and requires the stores
// whose writes it joins in one transaction.
//
// The tenant lock is optional and defaults to the in-process implementation. An
// assembly that supplies the same lock the policy module's binding delete and
// the membership guards take gets the serialisation those paths already rely
// on; a host that supplies none still serialises this module's own transfers
// and deletions.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenantcatalog.TxCatalog)(nil)},
			{Token: (*authzpolicy.TxManager)(nil)},
			{Token: (*provisioning.MembershipWriter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.InvitationRevoker)(nil), Optional: true},
			{Token: (*authz.TenantLock)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*notification.Dispatcher)(nil), Optional: true},
			{Token: (*tenant.Catalog)(nil), Optional: true},
			{Token: (*i18n.Profile)(nil), Optional: true},
			{Token: (*i18n.Resolver)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*tenant.OrganizationWriter)(nil), Exclusive: true},
			{Token: (*tenant.OwnershipTarget)(nil), Exclusive: true},
		},
		Resources: append([]any{
			db.Migrations{FS: migrationsFS},
			i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"},
		}, typeSpecResources()...),
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
	}
}

// product is the module's instance.
type product struct {
	database         db.Database
	txRoster         tenantcatalog.TxCatalog
	txManager        authzpolicy.TxManager
	membershipWriter provisioning.MembershipWriter
	accounts         authn.Accounts
	invitations      authn.InvitationRevoker
	lock             authz.TenantLock
	logger           *slog.Logger
	// dispatcher submits the request and result notices. It is optional: a
	// process that names no delivery pipeline still records and confirms
	// requests, and the absence is logged once rather than failing the write.
	dispatcher notification.Dispatcher
	// roster enters the tenant a notice is about, so the delivery pipeline can
	// write its in-app row even when the organization is disabled. It is
	// optional for the same reason.
	roster tenant.Catalog
	// profiles read a recipient's stored language preference. They are optional
	// and non-exclusive: an assembly that carries none still notifies in the
	// deployment's default language when it carries the resolver below, and
	// skips the notice when it carries neither.
	profiles []i18n.Profile
	// resolver answers the deployment's configured default language. It is
	// optional: a notice needs a language, so an assembly that carries neither a
	// stored preference nor this resolver has none to send in and skips the
	// notice rather than guessing one.
	resolver i18n.Resolver
}

var (
	_ tenant.OrganizationWriter = (*product)(nil)
	_ tenant.OwnershipTarget    = (*product)(nil)
)

// typeSpecResources turns the notification declarations into the resources the
// descriptor carries. The registry collects resources by type, so each
// declaration is one entry.
func typeSpecResources() []any {
	out := make([]any, len(notificationTypes))
	for i, spec := range notificationTypes {
		out[i] = spec
	}
	return out
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// newProduct takes up what the descriptor requires.
func newProduct(reg *core.Registry) (*product, error) {
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	txRoster, err := core.Resolve[tenantcatalog.TxCatalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the roster's transaction-aware writes: %w", ModuleName, err)
	}
	txManager, err := core.Resolve[authzpolicy.TxManager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy transaction-aware writes: %w", ModuleName, err)
	}
	membershipWriter, err := core.Resolve[provisioning.MembershipWriter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the transaction-aware membership writes: %w", ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account read surface: %w", ModuleName, err)
	}
	return &product{
		database:         database,
		txRoster:         txRoster,
		txManager:        txManager,
		membershipWriter: membershipWriter,
		accounts:         accounts,
		lock:             authz.NewInProcessTenantLock(),
		logger:           log.Default(),
	}, nil
}

// wire takes up the optional tenant lock and logger.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	lock, found, err := optional[authz.TenantLock](reg, "the tenant lock")
	if err != nil {
		return err
	}
	if found {
		p.lock = lock
	}
	revoker, revokerFound, err := optional[authn.InvitationRevoker](reg, "the invitation revoker")
	if err != nil {
		return err
	}
	if revokerFound {
		p.invitations = revoker
	}
	dispatcher, dispatcherFound, err := optional[notification.Dispatcher](reg, "the notification dispatcher")
	if err != nil {
		return err
	}
	if dispatcherFound {
		p.dispatcher = dispatcher
	}
	roster, rosterFound, err := optional[tenant.Catalog](reg, "the tenant roster")
	if err != nil {
		return err
	}
	if rosterFound {
		p.roster = roster
	}
	p.profiles = core.ResolveAll[i18n.Profile](reg)
	resolver, resolverFound, err := optional[i18n.Resolver](reg, "the i18n resolver")
	if err != nil {
		return err
	}
	if resolverFound {
		p.resolver = resolver
	}
	return nil
}

// optional takes up a capability whose absence is a legal assembly.
func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

// checkTenant refuses an identifier outside the domain before any lock or
// transaction is taken.
func checkTenant(id tenant.ID) error {
	if !tenant.IsIdentifier(string(id)) {
		return fmt.Errorf("%w: identifier %q", tenant.ErrInvalidTenant, id)
	}
	return nil
}

// checkAccount refuses a target account that is not an account identifier: it
// cannot be a member of any tenant.
func checkAccount(target string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("%w: the target account is empty", tenant.ErrOwnerNotMember)
	}
	if _, err := authn.ParseAccountID(target); err != nil {
		return fmt.Errorf("%w: the target %q is not an account identifier: %w", tenant.ErrOwnerNotMember, target, err)
	}
	return nil
}

// runAfter runs the work the transaction-aware writes queued, once the
// transaction committed. A failure is a warning: the write is in the table and
// this process may serve a snapshot older than it.
func (p *product) runAfter(ctx context.Context, after []func(context.Context) error) {
	for _, fn := range after {
		if fn == nil {
			continue
		}
		if err := fn(ctx); err != nil {
			p.logger.Warn(ModuleName+": the work a write left for after the commit failed; this process "+
				"may serve a snapshot older than the write", "cause", err.Error())
		}
	}
}

// begin opens the transaction the two writes run in.
func (p *product) begin(ctx context.Context, body func(tx *gorm.DB) error) error {
	return p.database.DB().WithContext(ctx).Transaction(body)
}
