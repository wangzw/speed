package tenant

import (
	"context"
	"errors"
	"time"
)

// ErrOwnerNotMember reports a transfer whose target is not an active member of
// the tenant. A transfer hands governance to somebody who is already in the
// organization; it does not add them.
var ErrOwnerNotMember = errors.New("tenant: the new owner is not an active member of the organization")

// ErrOwnerInactive reports a transfer whose target is an active member but not
// an active account. A suspended, disabled or deleted account cannot govern an
// organization, so the transfer refuses before it writes anything.
var ErrOwnerInactive = errors.New("tenant: the new owner's account is not active")

// ErrOwnerChanged reports an organization write begun by an owner that no
// longer owned the organization when the write reached its transaction. It is
// how a transfer and an owner-initiated deletion that race end with exactly one
// of them taking effect, and how a transfer called from an owner read that has
// gone stale is refused instead of carried out.
var ErrOwnerChanged = errors.New("tenant: the caller is no longer the owner of the organization")

// The sentinels of the owner-transfer request. A transfer takes effect only
// after the target confirms it, so the request has a lifecycle of its own and
// each refusal below names one state of it.

// ErrTransferNotFound reports a request no row carries, or one outside the
// caller's visible range. A route answers it and ErrTransferNotTarget alike
// with a bare 404, so a caller cannot tell an unknown request from another
// account's.
var ErrTransferNotFound = errors.New("tenant: the owner-transfer request does not exist")

// ErrTransferNotTarget reports a request whose target is another account. A
// route answers it with the same 404 ErrTransferNotFound gets, revealing no
// existence.
var ErrTransferNotTarget = errors.New("tenant: the caller is not the target of the owner-transfer request")

// ErrTransferNotPending reports a request that reached a terminal state:
// confirmed, declined, cancelled or expired.
var ErrTransferNotPending = errors.New("tenant: the owner-transfer request is no longer pending")

// ErrTransferExpired reports a request whose confirmation window has passed.
var ErrTransferExpired = errors.New("tenant: the owner-transfer request has expired")

// ErrTransferAlreadyPending reports a second request for an organization that
// already has one pending. Ownership is not silently redirected to another
// target: the pending request must be cancelled or expire first.
var ErrTransferAlreadyPending = errors.New("tenant: the organization already has a pending owner-transfer request")

// ErrTransferTargetIsOwner reports a request naming the current owner as its
// target. Such a request would change nothing.
var ErrTransferTargetIsOwner = errors.New("tenant: the target is already the organization's owner")

// ErrNoOwner reports a transfer requested on a tenant that has no owner to
// transfer from, such as the platform's own tenant.
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
	// From is the account that owned the organization when the request was
	// made. Platform staff appear as the audit actor only; From is always the
	// roster's owner.
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

// OrganizationWriter performs the organization writes that span the roster, the
// policy store and the membership store: an owner-transfer request, its
// cancellation, and an organization deletion.
//
// The roster itself cannot carry these writes. A confirmed transfer changes the
// owner and the new owner's tenant-admin binding; the previous owner keeps its
// membership and every binding it held. A deletion writes the roster tombstone
// and removes the tenant's memberships, bindings, roles and grants. Both have
// to be one transaction, and only an assembly that holds every one of those
// stores can open it, which is why this is a host seam rather than a Catalog
// method.
type OrganizationWriter interface {
	// RequestTransfer records a pending request to make target the tenant's
	// owner. Ownership does not change. The target must be an active member of
	// the tenant whose account is active; a target that is already the owner, an
	// organization with no owner, a second pending request and a tenant that is a
	// tombstone are refused. expectedOwner is the owner the caller observed, or
	// empty when the caller is platform staff; a non-empty value that no longer
	// matches the roster row is refused with ErrOwnerChanged.
	RequestTransfer(ctx context.Context, id ID, target, expectedOwner string) (TransferRequest, error)

	// CancelTransfer cancels one pending request. The request must belong to the
	// tenant in id. expectedOwner has the same meaning as in RequestTransfer.
	CancelTransfer(ctx context.Context, id ID, requestID, expectedOwner string) error

	// PendingTransfer reads the organization's pending request. The second
	// result is false when there is none. An expired request is settled as
	// expired before the read, so it is never returned as pending.
	PendingTransfer(ctx context.Context, id ID) (TransferRequest, bool, error)

	// DeleteOrganization writes the tenant's tombstone and cleans the core
	// relations in the same transaction: its memberships, bindings, roles and
	// grants, and its pending owner-transfer requests, which are cancelled. The
	// name and the identifier stay with the tombstone and are never reusable.
	// expectedOwner is the owner the caller observed, or empty when the caller is
	// platform staff; a non-empty value that no longer matches the row is
	// refused with ErrOwnerChanged.
	DeleteOrganization(ctx context.Context, id ID, expectedOwner string) error
}

// OwnershipTarget is the target's own side of an owner-transfer request. Every
// method acts on the caller's identity alone: the target does not need the
// organization in its request context, an organization-administration
// permission, or membership of the organization's current tenant.
type OwnershipTarget interface {
	// ConfirmTransfer confirms one pending request and writes the transfer. It
	// re-reads the request, checks that the requester is still the owner and that
	// the target is still an active member, writes the new owner's tenant-admin
	// binding when missing, writes the roster's owner column and marks the request
	// confirmed, all in one transaction. The previous owner stays an active member
	// and keeps every binding it held. It is the only path that writes the owner
	// column from a request.
	ConfirmTransfer(ctx context.Context, requestID, target string) error

	// DeclineTransfer declines one pending request. Ownership does not change.
	DeclineTransfer(ctx context.Context, requestID, target string) error

	// PendingTransfersFor reads the caller's pending requests across every
	// organization. Expired requests are settled as expired before the read.
	PendingTransfersFor(ctx context.Context, target string) ([]TransferRequest, error)

	// TransferFor reads one request the caller is the target of, in any state, so
	// a surface can show how a request ended. A request that does not exist and
	// one addressed to another account are refused with ErrTransferNotFound and
	// ErrTransferNotTarget respectively; a route answers both alike.
	TransferFor(ctx context.Context, requestID, target string) (TransferRequest, error)
}
