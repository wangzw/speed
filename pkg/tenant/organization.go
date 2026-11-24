package tenant

import (
	"context"
	"errors"
	"time"
)

// ErrOwnerNotMember reports a transfer whose target is not an active member of
// the tenant.
var ErrOwnerNotMember = errors.New("tenant: the new owner is not an active member of the organization")

// ErrOwnerInactive reports a transfer whose target is a member but not an
// active account.
var ErrOwnerInactive = errors.New("tenant: the new owner's account is not active")

// ErrOwnerChanged reports an organization write begun by an owner that no
// longer owned the organization when the write reached its transaction.
var ErrOwnerChanged = errors.New("tenant: the caller is no longer the owner of the organization")

// ErrTransferNotFound reports a request no row carries, or one outside the
// caller's visible range.
var ErrTransferNotFound = errors.New("tenant: the owner-transfer request does not exist")

// ErrTransferNotTarget reports a request whose target is another account.
var ErrTransferNotTarget = errors.New("tenant: the caller is not the target of the owner-transfer request")

// ErrTransferNotPending reports a request that reached a terminal state.
var ErrTransferNotPending = errors.New("tenant: the owner-transfer request is no longer pending")

// ErrTransferExpired reports a request whose confirmation window has passed.
var ErrTransferExpired = errors.New("tenant: the owner-transfer request has expired")

// ErrTransferAlreadyPending reports a second request for an organization that
// already has one pending.
var ErrTransferAlreadyPending = errors.New("tenant: the organization already has a pending owner-transfer request")

// ErrTransferTargetIsOwner reports a request naming the current owner as its
// target.
var ErrTransferTargetIsOwner = errors.New("tenant: the target is already the organization's owner")

// ErrNoOwner reports a transfer requested on a tenant that has no owner.
var ErrNoOwner = errors.New("tenant: the organization has no owner to transfer")

// TransferStatus is the state of one owner-transfer request. The set is closed.
type TransferStatus string

const (
	// TransferPending is a request the target has not answered yet.
	TransferPending TransferStatus = "pending"
	// TransferConfirmed is a request the target confirmed; ownership moved.
	TransferConfirmed TransferStatus = "confirmed"
	// TransferDeclined is a request the target declined.
	TransferDeclined TransferStatus = "declined"
	// TransferCancelled is a request the current owner or platform staff cancelled.
	TransferCancelled TransferStatus = "cancelled"
	// TransferExpired is a request whose window passed without an answer.
	TransferExpired TransferStatus = "expired"
)

// TransferRequest is one owner-transfer request.
type TransferRequest struct {
	// ID is the request identifier.
	ID string
	// Tenant is the organization whose ownership is the subject.
	Tenant ID
	// From is the roster's owner when the request was made.
	From string
	// To is the account the request names as the next owner.
	To string
	// RequestedAt is when the request was written.
	RequestedAt time.Time
	// ExpiresAt is the end of the confirmation window.
	ExpiresAt time.Time
	// Status is the request's state.
	Status TransferStatus
	// ResolvedAt is when the request reached a terminal state; nil while pending.
	ResolvedAt *time.Time
}

// OrganizationWriter performs the organization writes that span the roster, the
// policy store and the membership store. A host implements it.
type OrganizationWriter interface {
	// RequestTransfer records a pending request to make target the tenant's
	// owner; ownership does not change. expectedOwner is the owner the caller
	// observed, or empty for platform staff; a non-empty value that no longer
	// matches the roster row is refused with ErrOwnerChanged.
	RequestTransfer(ctx context.Context, id ID, target, expectedOwner string) (TransferRequest, error)

	// CancelTransfer cancels one pending request of the tenant in id.
	CancelTransfer(ctx context.Context, id ID, requestID, expectedOwner string) error

	// PendingTransfer reads the organization's pending request. The second
	// result is false when there is none.
	PendingTransfer(ctx context.Context, id ID) (TransferRequest, bool, error)

	// DeleteOrganization writes the tenant's tombstone and removes its
	// memberships, bindings, roles and grants in one transaction, cancelling its
	// pending transfer requests.
	DeleteOrganization(ctx context.Context, id ID, expectedOwner string) error
}

// OwnershipTarget is the target's side of an owner-transfer request. Every
// method acts on the caller's identity alone.
type OwnershipTarget interface {
	// ConfirmTransfer confirms one pending request and writes the transfer in
	// one transaction.
	ConfirmTransfer(ctx context.Context, requestID, target string) error

	// DeclineTransfer declines one pending request.
	DeclineTransfer(ctx context.Context, requestID, target string) error

	// PendingTransfersFor reads the caller's pending requests across every
	// organization.
	PendingTransfersFor(ctx context.Context, target string) ([]TransferRequest, error)

	// TransferFor reads one request the caller is the target of, in any state.
	// It returns ErrTransferNotFound or ErrTransferNotTarget otherwise.
	TransferFor(ctx context.Context, requestID, target string) (TransferRequest, error)
}
