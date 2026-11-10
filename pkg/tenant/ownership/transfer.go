package ownership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// RequestTransfer implements tenant.OrganizationWriter.
//
// It records a pending request and changes nothing else: the roster's owner
// column, the target's bindings and the previous owner's membership stay as they
// are until the target confirms. The target must be an active account and an
// active member of the tenant; the organization must have an owner; and the
// caller's expectation, when it has one, must still match the roster row. An
// existing pending request is refused rather than replaced, so a second request
// cannot silently redirect an offer the target has not answered.
//
// The tenant lock is taken before the target's account read, and the membership
// read inside the transaction takes the account's row lock, so a suspension
// that commits before this call either is seen here or waits for it.
func (p *product) RequestTransfer(ctx context.Context, id tenant.ID, target, expectedOwner string) (tenant.TransferRequest, error) {
	if err := checkTenant(id); err != nil {
		return tenant.TransferRequest{}, err
	}
	if err := checkAccount(target); err != nil {
		return tenant.TransferRequest{}, err
	}
	now := time.Now().UTC()
	release, err := p.lock.Lock(ctx, string(id))
	if err != nil {
		return tenant.TransferRequest{}, fmt.Errorf("%s: taking the tenant lock: %w", ModuleName, err)
	}
	defer release()

	if checkErr := p.requireActiveAccount(ctx, target); checkErr != nil {
		return tenant.TransferRequest{}, checkErr
	}
	if checkErr := p.settleExpiredForTenant(ctx, id, now); checkErr != nil {
		return tenant.TransferRequest{}, checkErr
	}

	request := tenant.TransferRequest{
		ID:          uuid.NewString(),
		Tenant:      id,
		To:          target,
		RequestedAt: now,
		ExpiresAt:   now.Add(transferRequestTTL),
		Status:      tenant.TransferPending,
	}
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
		if held.OwnerAccount == "" {
			return fmt.Errorf("%w: identifier %q", tenant.ErrNoOwner, id)
		}
		if held.OwnerAccount == target {
			return fmt.Errorf("%w: identifier %q", tenant.ErrTransferTargetIsOwner, id)
		}
		request.From = held.OwnerAccount

		member, found, readErr := p.membershipWriter.MembershipTx(ctx, tx, string(id), authn.AccountID(target))
		if readErr != nil {
			return fmt.Errorf("%s: reading the target's membership: %w", ModuleName, readErr)
		}
		if !found || !member.Active {
			return fmt.Errorf("%w: account %q is not an active member of tenant %q",
				tenant.ErrOwnerNotMember, target, id)
		}

		row := transferRow{
			ID:          request.ID,
			TenantID:    string(id),
			FromAccount: request.From,
			ToAccount:   target,
			Status:      statusPending,
			RequestedAt: request.RequestedAt,
			ExpiresAt:   request.ExpiresAt,
		}
		if writeErr := tx.WithContext(ctx).Create(&row).Error; writeErr != nil {
			if isUniqueViolation(writeErr) {
				return fmt.Errorf("%w: identifier %q", tenant.ErrTransferAlreadyPending, id)
			}
			return writeErr
		}
		return nil
	})
	if err != nil {
		if isTransferRefusal(err) {
			return tenant.TransferRequest{}, err
		}
		return tenant.TransferRequest{}, fmt.Errorf("%s: requesting the owner transfer of tenant %q: %w", ModuleName, id, err)
	}
	p.notify(ctx, id, notificationTransferRequested, target, map[string]any{
		"tenant":     string(id),
		"request":    request.ID,
		"expires_at": request.ExpiresAt,
	}, request.ID)
	return request, nil
}

// CancelTransfer implements tenant.OrganizationWriter.
//
// The request must belong to the tenant in id and still be pending. The caller's
// expectation, when it has one, must still match the roster row: an owner who
// has been handed on cannot cancel the new owner's request. No result
// notification is sent; cancelling is the requester's own action.
func (p *product) CancelTransfer(ctx context.Context, id tenant.ID, requestID, expectedOwner string) error {
	if err := checkTenant(id); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := p.settleExpiredRequest(ctx, requestID, now); err != nil {
		return err
	}
	row, found, err := p.getRequest(ctx, p.database.DB(), requestID)
	if err != nil {
		return err
	}
	if !found || tenant.ID(row.TenantID) != id {
		return fmt.Errorf("%w: request %q", tenant.ErrTransferNotFound, requestID)
	}
	if checkErr := refusalOf(row, now); checkErr != nil {
		return checkErr
	}

	release, err := p.lock.Lock(ctx, string(id))
	if err != nil {
		return fmt.Errorf("%s: taking the tenant lock: %w", ModuleName, err)
	}
	defer release()

	err = p.begin(ctx, func(tx *gorm.DB) error {
		if expectedOwner != "" {
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
			if held.OwnerAccount != expectedOwner {
				return fmt.Errorf("%w: identifier %q", tenant.ErrOwnerChanged, id)
			}
		}
		resolved, writeErr := resolveRequest(ctx, tx, requestID, statusCancelled, now)
		if writeErr != nil {
			return writeErr
		}
		if !resolved {
			return fmt.Errorf("%w: request %q", tenant.ErrTransferNotPending, requestID)
		}
		return nil
	})
	if err != nil {
		if isTransferRefusal(err) {
			return err
		}
		return fmt.Errorf("%s: cancelling the owner-transfer request %q: %w", ModuleName, requestID, err)
	}
	return nil
}

// PendingTransfer implements tenant.OrganizationWriter. Expired rows are
// settled first, so the answer never names a request whose window has passed.
func (p *product) PendingTransfer(ctx context.Context, id tenant.ID) (tenant.TransferRequest, bool, error) {
	if err := checkTenant(id); err != nil {
		return tenant.TransferRequest{}, false, err
	}
	if err := p.settleExpiredForTenant(ctx, id, time.Now().UTC()); err != nil {
		return tenant.TransferRequest{}, false, err
	}
	row, found, err := p.pendingRowForTenant(ctx, p.database.DB(), id)
	if err != nil || !found {
		return tenant.TransferRequest{}, false, err
	}
	return requestOf(row), true, nil
}

// ConfirmTransfer implements tenant.OwnershipTarget.
//
// This is the only path that writes the roster's owner column from a request.
// The request is re-read under the tenant lock, the target's account and
// membership and the requester's ownership are checked again, and the owner
// write, the tenant-admin binding, the request's confirmed state and the
// after-commit rereads are one transaction. A request another writer resolved
// first, or one whose window passed, is refused before anything is written.
func (p *product) ConfirmTransfer(ctx context.Context, requestID, target string) error {
	now := time.Now().UTC()
	if err := p.settleExpiredRequest(ctx, requestID, now); err != nil {
		return err
	}
	row, _, err := p.targetRow(ctx, requestID, target)
	if err != nil {
		return err
	}
	if checkErr := refusalOf(row, now); checkErr != nil {
		return checkErr
	}
	tenantID := tenant.ID(row.TenantID)
	release, err := p.lock.Lock(ctx, string(tenantID))
	if err != nil {
		return fmt.Errorf("%s: taking the tenant lock: %w", ModuleName, err)
	}
	defer release()

	// Re-read under the lock: another writer may have resolved the request
	// between the read above and the lock.
	row, _, err = p.getRequest(ctx, p.database.DB(), requestID)
	if err != nil {
		return err
	}
	if checkErr := refusalOf(row, now); checkErr != nil {
		return checkErr
	}
	if checkErr := p.requireActiveAccount(ctx, target); checkErr != nil {
		return checkErr
	}

	var after []func(context.Context) error
	err = p.begin(ctx, func(tx *gorm.DB) error {
		written, writeErr := p.confirmOwnerTx(ctx, tx, tenantID, target, row.FromAccount)
		if writeErr != nil {
			return writeErr
		}
		after = append(after, written...)
		resolved, writeErr := resolveRequest(ctx, tx, requestID, statusConfirmed, now)
		if writeErr != nil {
			return writeErr
		}
		if !resolved {
			return fmt.Errorf("%w: request %q", tenant.ErrTransferNotPending, requestID)
		}
		return nil
	})
	if err != nil {
		if isTransferRefusal(err) {
			return err
		}
		return fmt.Errorf("%s: confirming the owner-transfer request %q: %w", ModuleName, requestID, err)
	}
	p.runAfter(ctx, after)
	p.notify(ctx, tenantID, notificationTransferConfirmed, row.FromAccount, map[string]any{
		"tenant": string(tenantID),
	}, requestID+":confirmed")
	return nil
}

// DeclineTransfer implements tenant.OwnershipTarget. Ownership does not change;
// the requester is told the request ended.
func (p *product) DeclineTransfer(ctx context.Context, requestID, target string) error {
	now := time.Now().UTC()
	if err := p.settleExpiredRequest(ctx, requestID, now); err != nil {
		return err
	}
	row, _, err := p.targetRow(ctx, requestID, target)
	if err != nil {
		return err
	}
	if checkErr := refusalOf(row, now); checkErr != nil {
		return checkErr
	}
	tenantID := tenant.ID(row.TenantID)
	release, err := p.lock.Lock(ctx, string(tenantID))
	if err != nil {
		return fmt.Errorf("%s: taking the tenant lock: %w", ModuleName, err)
	}
	defer release()

	tenantDB := p.database.DB()
	row, _, err = p.getRequest(ctx, tenantDB, requestID)
	if err != nil {
		return err
	}
	if checkErr := refusalOf(row, now); checkErr != nil {
		return checkErr
	}
	resolved, err := resolveRequest(ctx, tenantDB, requestID, statusDeclined, now)
	if err != nil {
		return fmt.Errorf("%s: declining the owner-transfer request %q: %w", ModuleName, requestID, err)
	}
	if !resolved {
		return fmt.Errorf("%w: request %q", tenant.ErrTransferNotPending, requestID)
	}
	p.notify(ctx, tenantID, notificationTransferDeclined, row.FromAccount, map[string]any{
		"tenant": string(tenantID),
	}, requestID+":declined")
	return nil
}

// PendingTransfersFor implements tenant.OwnershipTarget.
func (p *product) PendingTransfersFor(ctx context.Context, target string) ([]tenant.TransferRequest, error) {
	if err := checkAccount(target); err != nil {
		return nil, err
	}
	if err := p.settleExpiredForTarget(ctx, target, time.Now().UTC()); err != nil {
		return nil, err
	}
	rows, err := p.pendingRowsForTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	out := make([]tenant.TransferRequest, len(rows))
	for i, row := range rows {
		out[i] = requestOf(row)
	}
	return out, nil
}

// TransferFor implements tenant.OwnershipTarget.
func (p *product) TransferFor(ctx context.Context, requestID, target string) (tenant.TransferRequest, error) {
	if err := checkAccount(target); err != nil {
		return tenant.TransferRequest{}, err
	}
	if err := p.settleExpiredRequest(ctx, requestID, time.Now().UTC()); err != nil {
		return tenant.TransferRequest{}, err
	}
	row, _, err := p.targetRow(ctx, requestID, target)
	if err != nil {
		return tenant.TransferRequest{}, err
	}
	return requestOf(row), nil
}

// targetRow reads one request and holds it to the target. A request that does
// not exist is ErrTransferNotFound and one addressed to another account is
// ErrTransferNotTarget; a route answers both alike.
func (p *product) targetRow(ctx context.Context, requestID, target string) (transferRow, bool, error) {
	row, found, err := p.getRequest(ctx, p.database.DB(), requestID)
	if err != nil {
		return transferRow{}, false, err
	}
	if !found {
		return transferRow{}, false, fmt.Errorf("%w: request %q", tenant.ErrTransferNotFound, requestID)
	}
	if row.ToAccount != target {
		return transferRow{}, false, fmt.Errorf("%w: request %q", tenant.ErrTransferNotTarget, requestID)
	}
	return row, true, nil
}

// refusalOf refuses a request that cannot be acted on: one past its window is
// reported as expired, and any other terminal state as not pending.
func refusalOf(row transferRow, now time.Time) error {
	switch statusOf(row.Status) {
	case tenant.TransferPending:
		if !now.Before(row.ExpiresAt) {
			return fmt.Errorf("%w: request %q", tenant.ErrTransferExpired, row.ID)
		}
		return nil
	case tenant.TransferExpired:
		return fmt.Errorf("%w: request %q", tenant.ErrTransferExpired, row.ID)
	default:
		return fmt.Errorf("%w: request %q", tenant.ErrTransferNotPending, row.ID)
	}
}

// confirmOwnerTx is the transfer's write body: it re-reads the roster under the
// transaction, checks the requester still owns it, checks the target's
// membership under the account's row lock, grants tenant-admin when missing and
// writes the owner column. It returns the after-commit work the two writes
// queued. The caller marks the request confirmed in the same transaction.
func (p *product) confirmOwnerTx(ctx context.Context, tx *gorm.DB, id tenant.ID, target, expectedOwner string) ([]func(context.Context) error, error) {
	var after []func(context.Context) error
	held, found, err := p.txRoster.GetTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
	}
	if held.Status == tenant.StatusDeleted {
		return nil, fmt.Errorf("%w: identifier %q", tenant.ErrTenantDeleted, id)
	}
	if held.OwnerAccount != expectedOwner {
		return nil, fmt.Errorf("%w: identifier %q", tenant.ErrOwnerChanged, id)
	}

	member, found, err := p.membershipWriter.MembershipTx(ctx, tx, string(id), authn.AccountID(target))
	if err != nil {
		return nil, fmt.Errorf("%s: reading the target's membership: %w", ModuleName, err)
	}
	if !found || !member.Active {
		return nil, fmt.Errorf("%w: account %q is not an active member of tenant %q",
			tenant.ErrOwnerNotMember, target, id)
	}

	if held.OwnerAccount != target {
		granted, writeErr := p.txManager.PutBindingTx(ctx, tx, authz.Binding{
			Tenant:    string(id),
			Account:   target,
			Role:      authz.Role(authz.TenantAdminRole),
			GrantedAt: time.Now().UTC(),
		})
		if writeErr != nil {
			return nil, fmt.Errorf("granting the tenant-admin role to the new owner: %w", writeErr)
		}
		after = append(after, granted)
	}

	owner, writeErr := p.txRoster.SetOwnerTx(ctx, tx, id, target)
	if writeErr != nil {
		return nil, writeErr
	}
	after = append(after, owner)
	return after, nil
}

// requireActiveAccount reads the target's account and refuses one that is not
// active. The read is made under the tenant lock; a suspension takes the same
// lock once the account is an active member of the tenant, so the status cannot
// change before the request or confirmation commits.
func (p *product) requireActiveAccount(ctx context.Context, target string) error {
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
	return nil
}

// isTransferRefusal reports whether an error is one of the sentinels a caller
// branches on, so it is returned as it stands rather than wrapped again.
func isTransferRefusal(err error) bool {
	for _, sentinel := range []error{
		tenant.ErrInvalidTenant, tenant.ErrTenantDeleted, tenant.ErrOwnerNotMember,
		tenant.ErrOwnerInactive, tenant.ErrOwnerChanged, tenant.ErrNoOwner,
		tenant.ErrTransferNotFound, tenant.ErrTransferNotTarget, tenant.ErrTransferNotPending,
		tenant.ErrTransferExpired, tenant.ErrTransferAlreadyPending, tenant.ErrTransferTargetIsOwner,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
