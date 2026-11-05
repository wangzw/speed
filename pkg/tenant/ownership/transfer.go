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
// The target must be an active account and an active member of the tenant. The
// transfer grants it the tenant-admin role whether or not it already holds the
// binding, writes the roster's owner column, and then withdraws the previous
// owner: every binding the previous owner held in the tenant is removed and its
// membership is deleted. All of it runs in one transaction, under the tenant's
// lock, so a concurrent deletion of the same organization either runs before
// this call and leaves a tombstone this call refuses, or runs after it and sees
// the new owner.
//
// The roster is read before the membership, so an identifier no row carries is
// ErrInvalidTenant and a tombstone is ErrTenantDeleted, not the
// ErrOwnerNotMember a membership read against a tenant that does not exist
// would otherwise give.
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

	// The account is read under the tenant lock, before the transaction opens.
	// A suspension or a deletion of the target takes the same tenant lock once
	// the target is an active member of it, so the status this read sees cannot
	// change before the transfer commits. Reading it through the account seam
	// keeps this module off the account table.
	account, err := p.accounts.Get(ctx, authn.AccountID(target))
	if errors.Is(err, authn.ErrAccountNotFound) {
		return fmt.Errorf("%w: account %q", tenant.ErrOwnerInactive, target)
	}
	if err != nil {
		return fmt.Errorf("%s: reading the target's account: %w", ModuleName, err)
	}
	if account.Status != authn.StatusActive {
		return fmt.Errorf("%w: account %q is %s", tenant.ErrOwnerInactive, target, account.Status)
	}

	var after []func(context.Context) error
	err = p.begin(ctx, func(tx *gorm.DB) error {
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
			errors.Is(err, tenant.ErrOwnerNotMember) || errors.Is(err, tenant.ErrOwnerInactive) {
			return err
		}
		return fmt.Errorf("%s: transferring the owner of tenant %q: %w", ModuleName, id, err)
	}
	p.runAfter(ctx, after)
	return nil
}
