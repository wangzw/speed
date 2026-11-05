package ownership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// TransferOwner implements tenant.OrganizationWriter.
//
// The target must be an active member of the tenant. The transfer grants it the
// tenant-admin role whether or not it already holds the binding, writes the
// roster's owner column, and then withdraws the previous owner: every binding
// the previous owner held in the tenant is removed and its membership is
// deleted. All of it runs in one transaction, under the tenant's lock, so a
// concurrent deletion of the same organization either runs before this call and
// leaves a tombstone this call refuses, or runs after it and sees the new
// owner.
func (p *product) TransferOwner(ctx context.Context, id tenant.ID, target string) error {
	if err := checkTenant(id); err != nil {
		return err
	}
	if err := checkAccount(target); err != nil {
		return err
	}
	release, err := p.lock.Lock(ctx, string(id))
	if err != nil {
		return fmt.Errorf("%s: taking the tenant lock: %w", ModuleName, err)
	}
	defer release()

	var after []func(context.Context) error
	err = p.begin(ctx, func(tx *gorm.DB) error {
		// The target's membership is read under the account's row lock inside
		// the transaction, so a suspension that commits before the transfer's
		// commit either is seen here or waits for it: the owner this writes is
		// an active member at the moment the row is set.
		member, found, readErr := p.membershipWriter.MembershipTx(ctx, tx, string(id), authn.AccountID(target))
		if readErr != nil {
			return fmt.Errorf("%s: reading the target's membership: %w", ModuleName, readErr)
		}
		if !found || !member.Active {
			return fmt.Errorf("%w: account %q is not an active member of tenant %q",
				tenant.ErrOwnerNotMember, target, id)
		}

		held, found, readErr := p.txRoster.GetTx(ctx, tx, id)
		if readErr != nil {
			return readErr
		}
		if !found {
			return fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
		}
		if held.Status == tenant.StatusDeleted {
			return fmt.Errorf("%w: identifier %q", tenant.ErrTenantDeleted, id)
		}
		previous := held.OwnerAccount
		if previous == target {
			return nil
		}

		granted, writeErr := p.txManager.PutBindingTx(ctx, tx, authz.Binding{
			Tenant:    string(id),
			Account:   target,
			Role:      authz.Role(authz.TenantAdminRole),
			GrantedAt: time.Now().UTC(),
		})
		if writeErr != nil {
			return fmt.Errorf("granting the tenant-admin role to the new owner: %w", writeErr)
		}
		after = append(after, granted)

		owner, writeErr := p.txRoster.SetOwnerTx(ctx, tx, id, target)
		if writeErr != nil {
			return writeErr
		}
		after = append(after, owner)

		if previous == "" {
			return nil
		}
		cleared, writeErr := p.txManager.ClearAccountBindingsTx(ctx, tx, string(id), previous)
		if writeErr != nil {
			return fmt.Errorf("removing the previous owner's bindings: %w", writeErr)
		}
		after = append(after, cleared)

		removed, writeErr := p.membershipWriter.RemoveMembershipTx(ctx, tx, authn.AccountID(previous), string(id))
		if writeErr != nil {
			return fmt.Errorf("removing the previous owner's membership: %w", writeErr)
		}
		after = append(after, removed)
		return nil
	})
	if err != nil {
		if errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDeleted) ||
			errors.Is(err, tenant.ErrOwnerNotMember) {
			return err
		}
		return fmt.Errorf("%s: transferring the owner of tenant %q: %w", ModuleName, id, err)
	}
	p.runAfter(ctx, after)
	return nil
}
