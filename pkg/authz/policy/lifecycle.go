package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/event"
)

// AccountCreated implements authn.AccountLifecycle. A new account holds no
// binding yet; the binding its creator is given belongs to the tenant that
// creator builds, not to this module. The hook is a no-op and idempotent.
func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled implements authn.AccountLifecycle. This module holds nothing
// keyed by an account's enabled state: a disabled account's bindings stay, and
// the account module is what refuses a disabled account before a request asks
// this module anything. The hook is a no-op and idempotent.
func (p *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled implements authn.AccountLifecycle. A disable leaves the
// bindings, so enabling has nothing to give back: the account module stops
// refusing the account and the bindings are in force again. The hook is a no-op
// and idempotent.
func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// AccountDeleted implements authn.AccountLifecycle: it removes every binding
// row the account holds, across every tenant.
//
// The cleanup is idempotent — deleting rows that are already gone is no work —
// and it crosses tenants, because an account is one subject in whichever
// tenants it holds roles and the account module's deletion is not scoped to
// one. A binding row that outlives the deletion names an account the
// authentication side can no longer produce, so the account is unusable there
// whether or not the row arrived; the cleanup is what keeps the table from
// growing rows for accounts that are gone.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	err := p.crossing(ctx).Where("account_id = ?", string(id)).Delete(&bindingRow{}).Error
	if err != nil {
		return fmt.Errorf("authz/policy: clearing the bindings of the deleted account %q: %w", id, err)
	}
	return p.afterWrite(ctx)
}

// ServiceSecretRevoked implements authn.AccountLifecycle. A service account's
// secrets are the authentication side's credentials and hold no policy of their
// own, so revoking one leaves this module's rows as they were.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

// MembershipRemoved implements authn.MembershipLifecycle: removing an account's
// membership of the platform tenant withdraws every binding the account holds in
// the platform domain.
//
// The platform administrator role exists only in the platform domain and a
// binding to it is refused unless the account is an active member of that
// tenant. Removing the membership has to withdraw the bindings too, or a row
// granted under a condition that has ceased keeps granting the role. The hook
// runs on the ordinary path, right after the removal commits, and the
// membership.changed subscription stays as the compensation for a crash between
// the commit and this call. A removal in a business tenant cascades nothing.
//
// The cleanup is idempotent: the account already holding nothing in the domain
// is no work and no error.
func (p *product) MembershipRemoved(ctx context.Context, id authn.AccountID, tenant string) error {
	if tenant != string(authz.PlatformDomain) {
		return nil
	}
	return p.withdrawPlatformBindings(ctx, id.String())
}

// CheckMembershipRemoval implements authn.MembershipGuard: an explicit removal of
// the account's membership is refused when the account is the tenant's only
// holder of the tenant-administrator role, because the tenant would be left with
// nobody who may govern it.
//
// The answer reads the bindings the way DeleteBinding's own floor does, and it
// counts bindings rather than active memberships: the rule is that a domain
// keeps an administrator binding, and a suspended member's binding still exists.
// It runs before the removal's own transaction, so the read never shares one
// with the membership store.
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

// withdrawPlatformBindings removes every binding the account holds in the
// platform domain in one statement. It is the cleanup both the synchronous hook
// and the membership.changed subscription run, so the ordinary path and the
// crash-window compensation cannot diverge. A second run deletes nothing and is
// no error.
func (p *product) withdrawPlatformBindings(ctx context.Context, account string) error {
	res := p.crossing(ctx).
		Where("tenant_id = ? AND account_id = ?", string(authz.PlatformDomain), account).
		Delete(&bindingRow{})
	if res.Error != nil {
		return fmt.Errorf("authz/policy: withdrawing the platform-domain bindings of %q: %w", account, res.Error)
	}
	return p.afterWrite(ctx)
}

// membershipChangeRemoved is the payload's change value for a membership that
// was deleted. The publisher's change values are text agreed between the two
// ends, and the authentication package documents this one beside the event.
const membershipChangeRemoved = "removed"

// onMembershipChanged withdraws an account's platform-domain bindings when its
// membership of the platform tenant is removed.
//
// The subscription is the compensation for a process that crashed between the
// removal's commit and MembershipRemoved, and for a replica that did not make
// the removal. Only a removal cascades: a suspension is a state the account can
// be resumed from, and the event names the tenant in its envelope, so a change
// in a business tenant leaves the platform domain alone.
//
// The handler is idempotent — a second delivery deletes nothing and still
// reloads the snapshot. A payload that cannot be read names no account, so there
// is nothing to withdraw and it is dropped, as the event contract requires of a
// subscriber.
func (p *product) onMembershipChanged(ctx context.Context, evt event.Event) error {
	if evt.TenantID != string(authz.PlatformDomain) {
		return nil
	}
	var payload authn.MembershipChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return nil
	}
	if payload.Change != membershipChangeRemoved || payload.AccountID == "" {
		return nil
	}
	return p.withdrawPlatformBindings(ctx, payload.AccountID)
}
