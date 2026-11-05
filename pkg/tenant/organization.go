package tenant

import (
	"context"
	"errors"
)

// ErrOwnerNotMember reports a transfer whose target is not an active member of
// the tenant. A transfer hands governance to somebody who is already in the
// organization; it does not add them.
var ErrOwnerNotMember = errors.New("tenant: the new owner is not an active member of the organization")

// ErrOwnerChanged reports an organization deletion begun by an owner that no
// longer owned the organization when the deletion reached its transaction. It
// is how a transfer and an owner-initiated deletion that race end with exactly
// one of them taking effect.
var ErrOwnerChanged = errors.New("tenant: the caller is no longer the owner of the organization")

// OrganizationWriter performs the organization writes that span the roster, the
// policy store and the membership store: an owner transfer and an organization
// deletion.
//
// The roster itself cannot carry these writes. A transfer changes the owner,
// the new owner's tenant-admin binding and the previous owner's binding and
// membership; a deletion writes the roster tombstone and removes the tenant's
// memberships, bindings, roles and grants. Both have to be one transaction, and
// only an assembly that holds every one of those stores can open it, which is
// why this is a host seam rather than a Catalog method.
type OrganizationWriter interface {
	// TransferOwner makes target the tenant's owner. The target must be an
	// active member of the tenant, and the transfer grants tenant-admin when the
	// target does not hold it. The previous owner loses every binding in the
	// tenant and is removed from it, in the same transaction. A refusal names
	// ErrOwnerNotMember, ErrInvalidTenant or ErrTenantDeleted.
	TransferOwner(ctx context.Context, id ID, target string) error

	// DeleteOrganization writes the tenant's tombstone and cleans the core
	// relations in the same transaction: its memberships, bindings, roles and
	// grants. The alias and the identifier stay with the tombstone and are never
	// reusable. expectedOwner is the owner the caller observed, or empty when the
	// caller is platform staff; a non-empty value that no longer matches the row
	// is refused with ErrOwnerChanged, so a transfer that won the race cannot be
	// followed by the old owner's deletion.
	DeleteOrganization(ctx context.Context, id ID, expectedOwner string) error
}
