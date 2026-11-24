package ownership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// DeleteOrganization implements tenant.OrganizationWriter: the roster row becomes a tombstone and
// the tenant's memberships, bindings, roles and grants are removed in the same transaction, while
// the identifier and name stay. A stale expectedOwner is ErrOwnerChanged.
func (p *product) DeleteOrganization(ctx context.Context, id tenant.ID, expectedOwner string) error {
	if err := checkTenant(id); err != nil {
		return err
	}
	release, err := p.lock.Lock(ctx, string(id))
	if err != nil {
		return fmt.Errorf("%s: taking the tenant lock: %w", ModuleName, err)
	}
	defer release()

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
		if expectedOwner != "" && held.OwnerAccount != expectedOwner {
			return fmt.Errorf("%w: identifier %q", tenant.ErrOwnerChanged, id)
		}

		tombstone, writeErr := p.txRoster.DeleteTx(ctx, tx, id)
		if writeErr != nil {
			return writeErr
		}
		after = append(after, tombstone)

		policy, writeErr := p.txManager.DeleteTenantPolicyTx(ctx, tx, string(id))
		if writeErr != nil {
			return fmt.Errorf("removing the organization's policy rows: %w", writeErr)
		}
		after = append(after, policy)

		memberships, writeErr := p.membershipWriter.RemoveTenantMembershipsTx(ctx, tx, string(id))
		if writeErr != nil {
			return fmt.Errorf("removing the organization's memberships: %w", writeErr)
		}
		after = append(after, memberships)

		now := time.Now().UTC()
		if writeErr := tx.WithContext(ctx).Model(&transferRow{}).
			Where("tenant_id = ? AND status = ?", string(id), statusPending).
			Updates(map[string]any{"status": statusCancelled, "resolved_at": now}).Error; writeErr != nil {
			return fmt.Errorf("cancelling the organization's pending owner-transfer requests: %w", writeErr)
		}
		if p.invitations != nil {
			after = append(after, func(ctx context.Context) error {
				return p.invitations.RevokeTenantInvitations(ctx, string(id))
			})
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDeleted) ||
			errors.Is(err, tenant.ErrOwnerChanged) {
			return err
		}
		return fmt.Errorf("%s: deleting tenant %q: %w", ModuleName, id, err)
	}
	p.runAfter(ctx, after)
	return nil
}
