package tenant

import (
	"context"
	"errors"
	"time"
)

// ErrOwnerNotMember reports a transfer whose target is not an active member of
// the tenant; a transfer does not add anyone.
var ErrOwnerNotMember = errors.New("tenant: the new owner is not an active member of the organization")

// ErrOwnerInactive reports a transfer whose target is an active member but not
// an active account.
var ErrOwnerInactive = errors.New("tenant: the new owner's account is not active")

// ErrOwnerChanged reports an organization write begun by an owner that no
// longer owned the organization when the write reached its transaction.
var ErrOwnerChanged = errors.New("tenant: the caller is no longer the owner of the organization")

// ErrTransferNotFound reports a request no row carries, or one outside the
// caller's visible range; a route answers it with a bare 404.
var ErrTransferNotFound = errors.New("tenant: the owner-transfer request does not exist")

// ErrTransferNotTarget reports a request whose target is another account,
// answered with the same 404 ErrTransferNotFound gets.
var ErrTransferNotTarget = errors.New("tenant: the caller is not the target of the owner-transfer request")

// ErrTransferNotPending reports a request that reached a terminal state.
var ErrTransferNotPending = errors.New("tenant: the owner-transfer request is no longer pending")

// ErrTransferExpired reports a request whose confirmation window has passed.
var ErrTransferExpired = errors.New("tenant: the owner-transfer request has expired")

// ErrTransferAlreadyPending reports a second request for an organization that
// already has one pending.
var ErrTransferAlreadyPending = errors.New("tenant: the organization already has a pending owner-transfer request")

// ErrTransferTargetIsOwner reports a request naming the current owner as its
// target, which would change nothing.
var ErrTransferTargetIsOwner = errors.New("tenant: the target is already the organization's owner")

// ErrNoOwner reports a transfer requested on a tenant that has no owner to
// transfer from.
var ErrNoOwner = errors.New("tenant: the organization has no owner to transfer")

// TransferStatus is the state of one owner-transfer request. The set is closed
// and every terminal state has no edge out of it.
type TransferStatus string

const (
	// TransferPending is a request the target has not answered yet. The current
	// owner keeps ownership while it is pending.
	TransferPending TransferStatus = "pending"
	// TransferConfirmed is a request the target confirmed; ownership moved.
	TransferConfirmed TransferStatus = "confirmed"
	// TransferDeclined is a request the target declined; ownership did not move.
	TransferDeclined TransferStatus = "declined"
	// TransferCancelled is a request the current owner or platform staff
	// cancelled; ownership did not move.
	TransferCancelled TransferStatus = "cancelled"
	// TransferExpired is a request whose window passed without an answer;
	// ownership did not move.
	TransferExpired TransferStatus = "expired"
)

// TransferRequest is one owner-transfer request: who asked to hand an
// organization to whom, when, and how it ended.
type TransferRequest struct {
	// ID is the request identifier.
	ID string
	// Tenant is the organization whose ownership is the subject.
	Tenant ID
	// From is the account that owned the organization when the request was made.
	// Platform staff appear as the audit actor only; From is always the roster's
	// owner.
	From string
	// To is the account the request names as the next owner.
	To string
	// RequestedAt is when the request was written.
	RequestedAt time.Time
	// ExpiresAt is the end of the confirmation window.
	ExpiresAt time.Time
	// Status is the request's state.
	Status TransferStatus
	// ResolvedAt is when the request reached a terminal state; nil while it is
	// pending.
	ResolvedAt *time.Time
}

// OrganizationWriter performs the organization writes that span the roster, the policy store and
// the membership store: an owner-transfer request, its cancellation, and an organization deletion.
// Each is one transaction, which only an assembly holding every one of those stores can open.
type OrganizationWriter interface {
	// RequestTransfer records a pending request to make target the tenant's owner; ownership does not
	// change. The target must be an active member whose account is active, and a second pending request or
	// a tombstone is refused. A non-empty expectedOwner that no longer matches is ErrOwnerChanged.
	RequestTransfer(ctx context.Context, id ID, target, expectedOwner string) (TransferRequest, error)

	// CancelTransfer cancels one pending request. The request must belong to the
	// tenant in id. expectedOwner has the same meaning as in RequestTransfer.
	CancelTransfer(ctx context.Context, id ID, requestID, expectedOwner string) error

	// PendingTransfer reads the organization's pending request. The second
	// result is false when there is none. An expired request is settled as
	// expired before the read, so it is never returned as pending.
	PendingTransfer(ctx context.Context, id ID) (TransferRequest, bool, error)

	// DeleteOrganization writes the tenant's tombstone and cleans its memberships, bindings, roles,
	// grants and pending owner-transfer requests in the same transaction. The name and identifier stay
	// with the tombstone. A non-empty expectedOwner that no longer matches is ErrOwnerChanged.
	DeleteOrganization(ctx context.Context, id ID, expectedOwner string) error
}

// OwnershipTarget is the target's own side of an owner-transfer request. Every
// method acts on the caller's identity alone: the target needs no request-context
// tenant, no administration permission, and no membership of the tenant.
type OwnershipTarget interface {
	// ConfirmTransfer confirms one pending request and writes the transfer in one transaction: it
	// re-reads the request, checks the requester is still the owner and the target an active member,
	// writes the new owner's binding and the owner column, and marks it confirmed.
	ConfirmTransfer(ctx context.Context, requestID, target string) error

	// DeclineTransfer declines one pending request. Ownership does not change.
	DeclineTransfer(ctx context.Context, requestID, target string) error

	// PendingTransfersFor reads the caller's pending requests across every
	// organization. Expired requests are settled as expired before the read.
	PendingTransfersFor(ctx context.Context, target string) ([]TransferRequest, error)

	// TransferFor reads one request the caller is the target of, in any state. A
	// request that does not exist is ErrTransferNotFound, and one addressed to
	// another account is ErrTransferNotTarget; a route answers both alike.
	TransferFor(ctx context.Context, requestID, target string) (TransferRequest, error)
}
