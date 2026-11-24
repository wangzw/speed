package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/event"
)

// AccountCreated implements authn.AccountLifecycle. A new account holds no
// binding, so the hook is a no-op and idempotent.
func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled implements authn.AccountLifecycle. Bindings stay, so the hook
// is a no-op and idempotent.
func (p *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled implements authn.AccountLifecycle. Enabling has nothing to give
// back, so the hook is a no-op and idempotent.
func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// AccountDeleted implements authn.AccountLifecycle: it removes every binding row
// the account holds, across every tenant. It is idempotent.
//
// The write takes no tenant lock: account deletion is the terminal erasure, not
// a removal that judges a domain's floor, and a lock here would re-enter one a
// membership removal already holds.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	err := p.crossing(ctx).Where("account_id = ?", string(id)).Delete(&bindingRow{}).Error
	if err != nil {
		return fmt.Errorf("authz/policy: clearing the bindings of the deleted account %q: %w", id, err)
	}
	return p.afterWrite(ctx)
}

// ServiceSecretRevoked implements authn.AccountLifecycle. A service account's
// secrets hold no policy, so this module's rows stay as they were.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

// MembershipRemoved implements authn.MembershipLifecycle: an explicit membership
// removal withdraws every binding the account holds in the tenant it left. A
// removal takes the roles with the membership; a suspended membership keeps its
// bindings. The hook is idempotent.
func (p *product) MembershipRemoved(ctx context.Context, id authn.AccountID, tenant string) error {
	if tenant == "" {
		return nil
	}
	return p.ClearAccountBindings(ctx, tenant, id.String())
}

// CheckMembershipRemoval implements authn.MembershipGuard: an explicit removal
// of the account's membership is refused when the account is the tenant's only
// holder of the tenant-administrator role. It counts bindings, not active
// memberships, and runs before the removal's own transaction.
func (p *product) CheckMembershipRemoval(ctx context.Context, id authn.AccountID, tenant string) error {
	if tenant == "" {
		return nil
	}
	rows, err := tenantAdminRows(ctx, p.handle, tenant, false)
	if err != nil {
		return err
	}
	if lastTenantAdminAfterRemoval(rows, id.String()) {
		return fmt.Errorf("%w: account %q is the last %q of tenant %q", authn.ErrLastTenantAdmin,
			id, authz.TenantAdminRole, tenant)
	}
	return nil
}

// membershipChangeRemoved is the payload's change value for a membership that
// was deleted.
const membershipChangeRemoved = "removed"

// onMembershipChanged withdraws the bindings an account holds in the tenant the
// envelope names, when its membership of that tenant is removed. It compensates
// for a crash between the removal's commit and MembershipRemoved. A payload that
// cannot be read is dropped.
func (p *product) onMembershipChanged(ctx context.Context, evt event.Event) error {
	var payload authn.MembershipChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return nil
	}
	if payload.Change != membershipChangeRemoved || payload.AccountID == "" || evt.TenantID == "" {
		return nil
	}
	return p.ClearAccountBindings(ctx, evt.TenantID, payload.AccountID)
}
