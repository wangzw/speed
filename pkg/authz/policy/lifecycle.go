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
//
// The write takes no tenant lock: account deletion is the terminal erasure of
// the account, not a removal that judges a domain's floor, and a lock here
// would re-enter one a membership removal already holds.
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

// MembershipRemoved implements authn.MembershipLifecycle: an explicit membership
// removal withdraws every binding the account holds in the tenant it left.
//
// Removal is the member leaving the tenant, not a pause: a suspended membership
// keeps its bindings and can be resumed, while a removal takes the roles away
// with the membership, so a rejoin starts from none. The cascade reaches the
// platform domain as well, where the platform administrator role exists only
// while the account is an active member of that tenant, and it is safe in a
// business tenant because the removal guard refuses the last tenant
// administrator: a tenant that stays in service keeps someone who may govern it.
//
// The hook runs on the ordinary path, right after the removal commits, and the
// membership.changed subscription stays as the compensation for a crash between
// the commit and this call. The clear is the manager's own binding-removal path,
// so the hook and the deletion cascade cannot diverge. It is idempotent: the
// account already holding nothing in the tenant is no work and no error.
func (p *product) MembershipRemoved(ctx context.Context, id authn.AccountID, tenant string) error {
	if tenant == "" {
		return nil
	}
	return p.ClearAccountBindings(ctx, tenant, id.String())
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

// membershipChangeRemoved is the payload's change value for a membership that
// was deleted. The publisher's change values are text agreed between the two
// ends, and the authentication package documents this one beside the event.
const membershipChangeRemoved = "removed"

// onMembershipChanged withdraws the bindings an account holds in the tenant the
// envelope names, when its membership of that tenant is removed.
//
// The subscription is the compensation for a process that crashed between the
// removal's commit and MembershipRemoved, and for a replica that did not make
// the removal. Only a removal cascades: a suspension is a state the account can
// be resumed from, and its bindings stay. The write is the same clear the hook
// runs and is idempotent — a second delivery deletes nothing and still reloads
// the snapshot. A payload that cannot be read names no account, so there is
// nothing to withdraw and it is dropped, as the event contract requires of a
// subscriber.
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
