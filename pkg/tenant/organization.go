package tenant

import (
	"context"
	"errors"
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

// OrganizationWriter performs the organization writes that span the roster, the
// policy store and the membership store: an owner transfer and an organization
// deletion.
//
// The roster itself cannot carry these writes. A transfer changes the owner
// and the new owner's tenant-admin binding; the previous owner keeps its
// membership and every binding it held. A deletion writes the roster tombstone
// and removes the tenant's memberships, bindings, roles and grants. Both have
// to be one transaction, and only an assembly that holds every one of those
// stores can open it, which is why this is a host seam rather than a Catalog
// method.
type OrganizationWriter interface {
	// TransferOwner makes target the tenant's owner. The target must be an
	// active member of the tenant, and the transfer grants tenant-admin when the
	// target does not hold it. The target's account must also be active: a
	// suspended, disabled or deleted account is refused with ErrOwnerInactive.
	// Ownership is the only thing the transfer changes: the previous owner stays
	// an active member and keeps every binding it held, tenant-admin included.
	// expectedOwner is the owner the caller observed,
	// or empty when the caller is platform staff; a non-empty value that no longer
	// matches the row is refused with ErrOwnerChanged, so an owner whose read of
	// the roster went stale cannot hand the organization on. A refusal names
	// ErrOwnerNotMember, ErrOwnerInactive, ErrOwnerChanged, ErrInvalidTenant or
	// ErrTenantDeleted.
	TransferOwner(ctx context.Context, id ID, target, expectedOwner string) error

	// DeleteOrganization writes the tenant's tombstone and cleans the core
	// relations in the same transaction: its memberships, bindings, roles and
	// grants. The name and the identifier stay with the tombstone and are never
	// reusable. expectedOwner is the owner the caller observed, or empty when the
	// caller is platform staff; a non-empty value that no longer matches the row
	// is refused with ErrOwnerChanged, so a transfer that won the race cannot be
	// followed by the old owner's deletion.
	DeleteOrganization(ctx context.Context, id ID, expectedOwner string) error
}
