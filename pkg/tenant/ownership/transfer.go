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

// RequestTransfer implements tenant.OrganizationWriter. It records a pending
// request and changes no ownership until the target confirms.
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

// PendingTransfer implements tenant.OrganizationWriter.
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

// DeclineTransfer implements tenant.OwnershipTarget.
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
