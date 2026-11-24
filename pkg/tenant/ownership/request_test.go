package ownership_test

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/tenant"
)

// TestRequestTransferDoesNotChangeOwnership checks that a request records a pending row, notifies the target and changes no ownership, binding or membership.
func TestRequestTransferDoesNotChangeOwnership(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if request.ID == "" || request.Status != tenant.TransferPending || request.From != owner.ID.String() ||
			request.To != target.ID.String() {
			t.Errorf("the request is %+v, want a pending request from %q to %q", request, owner.ID, target.ID)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 0 {
			t.Errorf("the request bound the target: %+v", bindings)
		}
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Fatalf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}

		mine, err := a.target.PendingTransfersFor(t.Context(), target.ID.String())
		if err != nil || len(mine) != 1 || mine[0].ID != request.ID {
			t.Fatalf("the target's pending list is %+v err=%v, want the request", mine, err)
		}
		if others, err := a.target.PendingTransfersFor(t.Context(), owner.ID.String()); err != nil || len(others) != 0 {
			t.Errorf("the owner's pending list is %+v err=%v, want none", others, err)
		}

		dispatched := a.dispatcher.all()
		if len(dispatched) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want one to the target", len(dispatched))
		}
		notice := dispatched[0]
		if notice.TypeKey != "tenant.owner.transfer_requested" ||
			notice.Recipient.Class != notificationRecipientUser || notice.Recipient.UserID != target.ID.String() {
			t.Errorf("the notice is %+v, want the request notice to the target", notice)
		}
		if notice.OccurrenceID != request.ID || notice.Params["request"] != request.ID {
			t.Errorf("the notice's occurrence is %+v, want the request identifier", notice)
		}
	})
}

const notificationRecipientUser = "user"

// TestASecondRequestIsRefusedWhileOneIsPending checks that a second request is refused while one is pending and does not replace it.
func TestASecondRequestIsRefusedWhileOneIsPending(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		third := a.user(t, "carol")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.join(t, "acme", third)

		first, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		_, err = a.writer.RequestTransfer(t.Context(), "acme", third.ID.String(), "")
		if !errors.Is(err, tenant.ErrTransferAlreadyPending) {
			t.Fatalf("the second request gave %v, want ErrTransferAlreadyPending", err)
		}
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != first.ID || pending.To != target.ID.String() {
			t.Fatalf("the pending read answered %+v found=%t err=%v, want the first request unchanged", pending, found, err)
		}
	})
}

// TestConfirmTransferLandsTheTransfer checks that confirming makes the target the owner with tenant-admin, keeps the previous owner's membership and bindings, and notifies the previous owner.
func TestConfirmTransferLandsTheTransfer(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.dispatcher.reset()

		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if checkErr := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); checkErr != nil {
			t.Fatalf("confirming the transfer: %v", checkErr)
		}

		if got := a.held(t, "acme").OwnerAccount; got != target.ID.String() {
			t.Errorf("the owner is %q, want %q", got, target.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 1 {
			t.Errorf("the target holds %+v, want tenant-admin", bindings)
		}
		if bindings := a.bindingsOf(t, "acme", owner); len(bindings) != 1 {
			t.Errorf("the previous owner holds %+v, want tenant-admin", bindings)
		}
		member, found, err := a.memberships.Membership(t.Context(), "acme", owner.ID)
		if err != nil || !found || !member.Active {
			t.Errorf("the previous owner's membership is %+v found=%t err=%v, want active", member, found, err)
		}
		dispatched := a.dispatcher.all()
		if len(dispatched) != 2 {
			t.Fatalf("the module dispatched %d notices, want the request and the result", len(dispatched))
		}
		result := dispatched[1]
		if result.TypeKey != "tenant.owner.transfer_confirmed" || result.Recipient.UserID != owner.ID.String() {
			t.Errorf("the result notice is %+v, want the confirmation to the previous owner", result)
		}
	})
}

// TestOnlyTheTargetActsOnARequest checks that another account gets ErrTransferNotTarget and an unknown request ErrTransferNotFound.
func TestOnlyTheTargetActsOnARequest(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		other := a.user(t, "carol")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.join(t, "acme", other)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}

		if err := a.target.ConfirmTransfer(t.Context(), request.ID, other.ID.String()); !errors.Is(err, tenant.ErrTransferNotTarget) {
			t.Errorf("another account's confirmation gave %v, want ErrTransferNotTarget", err)
		}
		if err := a.target.DeclineTransfer(t.Context(), request.ID, other.ID.String()); !errors.Is(err, tenant.ErrTransferNotTarget) {
			t.Errorf("another account's decline gave %v, want ErrTransferNotTarget", err)
		}
		if _, err := a.target.TransferFor(t.Context(), "no-such-request", target.ID.String()); !errors.Is(err, tenant.ErrTransferNotFound) {
			t.Errorf("an unknown request gave %v, want ErrTransferNotFound", err)
		}
	})
}

// TestDeclineLeavesTheOwnerUnchanged checks that declining leaves the owner unchanged and notifies the requester.
func TestDeclineLeavesTheOwnerUnchanged(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		a.dispatcher.reset()

		if checkErr := a.target.DeclineTransfer(t.Context(), request.ID, target.ID.String()); checkErr != nil {
			t.Fatalf("declining the transfer: %v", checkErr)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 0 {
			t.Errorf("the decline bound the target: %+v", bindings)
		}
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || found {
			t.Errorf("the pending read answered %+v found=%t err=%v, want none", pending, found, err)
		}
		stored, err := a.target.TransferFor(t.Context(), request.ID, target.ID.String())
		if err != nil || stored.Status != tenant.TransferDeclined {
			t.Errorf("the stored request is %+v err=%v, want declined", stored, err)
		}
		if err := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); !errors.Is(err, tenant.ErrTransferNotPending) {
			t.Errorf("confirming a declined request gave %v, want ErrTransferNotPending", err)
		}
		dispatched := a.dispatcher.all()
		if len(dispatched) != 1 || dispatched[0].TypeKey != "tenant.owner.transfer_declined" ||
			dispatched[0].Recipient.UserID != owner.ID.String() {
			t.Errorf("the notices are %+v, want the decline notice to the requester", dispatched)
		}
	})
}

// TestCancelRequiresTheCurrentOwner checks that cancelling succeeds for the observed owner and is refused with ErrOwnerChanged otherwise.
func TestCancelRequiresTheCurrentOwner(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		other := a.user(t, "carol")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if checkErr := a.writer.CancelTransfer(t.Context(), "acme", request.ID, other.ID.String()); !errors.Is(checkErr, tenant.ErrOwnerChanged) {
			t.Errorf("a non-owner's cancellation gave %v, want ErrOwnerChanged", checkErr)
		}
		if checkErr := a.writer.CancelTransfer(t.Context(), "acme", request.ID, owner.ID.String()); checkErr != nil {
			t.Fatalf("the owner's cancellation: %v", checkErr)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
		stored, err := a.target.TransferFor(t.Context(), request.ID, target.ID.String())
		if err != nil || stored.Status != tenant.TransferCancelled {
			t.Errorf("the stored request is %+v err=%v, want cancelled", stored, err)
		}
		if err := a.writer.CancelTransfer(t.Context(), "acme", request.ID, owner.ID.String()); !errors.Is(err, tenant.ErrTransferNotPending) {
			t.Errorf("cancelling a resolved request gave %v, want ErrTransferNotPending", err)
		}
	})
}

// TestAnExpiredRequestCannotBeConfirmed checks that a request past its window is settled as expired, is not reported as pending and cannot be confirmed.
func TestAnExpiredRequestCannotBeConfirmed(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if checkErr := a.handle.Table("tenant_owner_transfers").
			Where("id = ?", request.ID).
			Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; checkErr != nil {
			t.Fatalf("moving the request's window into the past: %v", checkErr)
		}

		if checkErr := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); !errors.Is(checkErr, tenant.ErrTransferExpired) {
			t.Fatalf("confirming an expired request gave %v, want ErrTransferExpired", checkErr)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the expired request changed the owner to %q, want %q", got, owner.ID)
		}
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || found {
			t.Errorf("the pending read answered %+v found=%t err=%v, want none", pending, found, err)
		}
		stored, err := a.target.TransferFor(t.Context(), request.ID, target.ID.String())
		if err != nil || stored.Status != tenant.TransferExpired {
			t.Errorf("the stored request is %+v err=%v, want expired", stored, err)
		}
		if list, err := a.target.PendingTransfersFor(t.Context(), target.ID.String()); err != nil || len(list) != 0 {
			t.Errorf("the target's pending list is %+v err=%v, want none", list, err)
		}
	})
}

// TestConfirmAfterTheTargetLeftTheTenantIsRefused checks that confirming is refused once the target is no longer an active member.
func TestConfirmAfterTheTargetLeftTheTenantIsRefused(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if err := a.memberships.Remove(t.Context(), target.ID, "acme"); err != nil {
			t.Fatalf("removing the target's membership: %v", err)
		}

		if err := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); !errors.Is(err, tenant.ErrOwnerNotMember) {
			t.Fatalf("confirming after the target left gave %v, want ErrOwnerNotMember", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
	})
}

// TestConfirmAfterTheTargetWasSuspendedIsRefused checks that confirming is refused once the target's account is suspended.
func TestConfirmAfterTheTargetWasSuspendedIsRefused(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if err := a.admin.SetStatus(t.Context(), target.ID, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("suspending the target: %v", err)
		}

		if err := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); !errors.Is(err, tenant.ErrOwnerInactive) {
			t.Fatalf("confirming after the target was suspended gave %v, want ErrOwnerInactive", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
	})
}

// TestDeleteCancelsThePendingRequest checks that deleting the organization cancels its pending request and drops it from the target's list.
func TestDeleteCancelsThePendingRequest(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if checkErr := a.writer.DeleteOrganization(t.Context(), "acme", ""); checkErr != nil {
			t.Fatalf("deleting the organization: %v", checkErr)
		}
		stored, err := a.target.TransferFor(t.Context(), request.ID, target.ID.String())
		if err != nil || stored.Status != tenant.TransferCancelled {
			t.Errorf("the stored request is %+v err=%v, want cancelled", stored, err)
		}
		if err := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); !errors.Is(err, tenant.ErrTransferNotPending) {
			t.Errorf("confirming a cancelled request gave %v, want ErrTransferNotPending", err)
		}
	})
}

// TestRequestTransferRefusesATenantWithNoOwner checks that a request for a tenant with no owner is refused with ErrNoOwner.
func TestRequestTransferRefusesATenantWithNoOwner(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "system", Name: "system", Status: tenant.StatusActive,
		}); err != nil {
			t.Fatalf("registering the system tenant: %v", err)
		}
		a.join(t, "system", target)
		a.join(t, "system", owner)

		_, err := a.writer.RequestTransfer(t.Context(), "system", target.ID.String(), "")
		if !errors.Is(err, tenant.ErrNoOwner) {
			t.Fatalf("requesting a transfer of an ownerless tenant gave %v, want ErrNoOwner", err)
		}
	})
}
