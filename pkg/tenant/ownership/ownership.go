// Package ownership performs the organization writes that span the roster, the
// policy store and the membership store: an owner transfer and an organization
// deletion.
//
// Neither write belongs to the roster alone. A transfer changes the roster's
// owner column, the new owner's tenant-admin binding and the previous owner's
// bindings and membership; a deletion writes the roster tombstone and removes
// the tenant's memberships, bindings, roles and grants. Both must stand or fall
// as one transaction, so they live in a module that holds the transaction-aware
// surfaces of all three stores rather than in any one of them.
//
// The module mounts no route and delivers no configurable behavior. Both the
// platform management surface and the organization's own tenant surface take
// the capability up and call it; the module cannot tell them apart.
package ownership

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.ownership"

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers
// tenant.OrganizationWriter and requires the stores whose writes it joins in
// one transaction.
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
			{Token: (*authz.TenantLock)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*tenant.OrganizationWriter)(nil)},
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
	}
}

// product is the module's instance.
type product struct {
	database         db.Database
	txRoster         tenantcatalog.TxCatalog
	txManager        authzpolicy.TxManager
	membershipWriter provisioning.MembershipWriter
	accounts         authn.Accounts
	lock             authz.TenantLock
	logger           *slog.Logger
}

var _ tenant.OrganizationWriter = (*product)(nil)

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
