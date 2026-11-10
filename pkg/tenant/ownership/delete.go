package ownership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// DeleteOrganization implements tenant.OrganizationWriter.
//
// The roster row becomes a tombstone and the tenant's core relations are
// removed in the same transaction: its memberships, its bindings, its roles and
// their grants. The identifier and the name stay with the tombstone, so the
// roster refuses to register either again. Data other modules hold for the
// tenant is not touched here; those modules clean it up under the tombstone.
//
// expectedOwner is the owner the caller observed, or empty for platform staff.
// A non-empty value that no longer matches the row is ErrOwnerChanged: a
// transfer that won the race leaves the old owner's deletion refused, and a
// deletion that won leaves the transfer's own tombstone refusal.
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

		// A deletion is terminal, so a pending request has nothing left to
		// confirm. It is cancelled in the same transaction as the tombstone: a
		// failure here rolls the whole deletion back.
		now := time.Now().UTC()
		if writeErr := tx.WithContext(ctx).Model(&transferRow{}).
			Where("tenant_id = ? AND status = ?", string(id), statusPending).
			Updates(map[string]any{"status": statusCancelled, "resolved_at": now}).Error; writeErr != nil {
			return fmt.Errorf("cancelling the organization's pending owner-transfer requests: %w", writeErr)
		}
		// A deleted organization has no grant left to hand out. The invitation
		// module owns its rows, so it is asked to withdraw the pending ones once
		// the deletion has committed; a failure is a warning and acceptance still
		// refuses the tenant because it is gone.
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
