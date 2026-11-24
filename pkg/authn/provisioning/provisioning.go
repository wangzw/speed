// Package provisioning declares the in-transaction seams an account needs
// other modules to satisfy.
package provisioning

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// AccountFacts is the account facts a provisioning method receives.
type AccountFacts struct {
	// ID is the account's identifier, the string form of authn.AccountID.
	ID string
	// Kind is "user" or "service", the account row's kind.
	Kind string
	// Status is "active", "disabled" or "deleted", the account row's status at
	// the moment the seam is called.
	Status string
	// Username is the account's normalized username, and empty for a service
	// account.
	Username string
	// DisplayName is the account's display name as the row carries it.
	DisplayName string
}

// AccountProvisioning writes what an account owns inside the transaction that
// creates or deletes it. The account module requires one implementation.
type AccountProvisioning interface {
	// ProvisionAccount writes what a user account owns for the whole of its life,
	// before the creation event is staged, and writes nothing for a service account.
	ProvisionAccount(ctx context.Context, tx *gorm.DB, facts AccountFacts) error
	// ProvisionAccountDeletion ends what ProvisionAccount wrote, keeping the
	// parts an identifier's history needs. It is called after the account's
	// own rows are erased, inside the same transaction, and must be idempotent.
	ProvisionAccountDeletion(ctx context.Context, tx *gorm.DB, facts AccountFacts) error
}

// MembershipWriter is the account module's transaction-aware membership surface.
// The account module delivers it exclusively.
type MembershipWriter interface {
	// MembershipTx reads one membership in the given transaction, locking the
	// account row first. It returns false when the account has no membership in
	// the tenant or does not exist.
	MembershipTx(ctx context.Context, tx *gorm.DB, tenant string, account authn.AccountID) (authn.Membership, bool, error)
	// PutMembershipTx writes a membership, and writing the same one again is no
	// change. It is Put's rules without the transaction Put opens.
	PutMembershipTx(ctx context.Context, tx *gorm.DB, m authn.Membership) (after func(context.Context) error, err error)
	// PutJoiningTx writes a joining membership with PutJoining's rules: only an
	// account with no membership or an active one ends active, and a suspended
	// one is never resumed. It reports whether the account is active afterwards.
	PutJoiningTx(ctx context.Context, tx *gorm.DB, m authn.Membership, wasActive bool) (joined bool, after func(context.Context) error, err error)
	// RemoveMembershipTx deletes a membership. One that is not there is already
	// removed and returns a nil closure. It is RemoveMembershipForDeletion's
	// rules without the transaction that method opens.
	RemoveMembershipTx(ctx context.Context, tx *gorm.DB, account authn.AccountID, tenant string) (after func(context.Context) error, err error)
	// RemoveTenantMembershipsTx deletes every membership of one tenant in the given
	// transaction, publishing authn.membership.changed for each removed row. No guard
	// is asked and no lifecycle hook runs; a tenant with no membership returns nil.
	RemoveTenantMembershipsTx(ctx context.Context, tx *gorm.DB, tenant string) (after func(context.Context) error, err error)
}

// RoleBinder is the transaction-aware role-binding write an acceptance joins, so
// the binding lands in the acceptance's own transaction.
type RoleBinder interface {
	// BindRoleTx binds role to account within tenant in the transaction it is
	// handed.
	BindRoleTx(ctx context.Context, tx *gorm.DB, tenant, account, role string) (after func(context.Context) error, err error)
}

type afterCommitKey struct{}

// WithAfterCommit returns a context carrying a fresh collector, and the
// collector. The caller runs it once the transaction it covers has committed.
func WithAfterCommit(ctx context.Context) (context.Context, *AfterCommit) {
	a := &AfterCommit{}
	return context.WithValue(ctx, afterCommitKey{}, a), a
}

// AfterCommitOf returns the collector the context carries, or nil. A nil
// collector drops its work, so a call outside the transaction needs no special case.
func AfterCommitOf(ctx context.Context) *AfterCommit {
	if a, ok := ctx.Value(afterCommitKey{}).(*AfterCommit); ok {
		return a
	}
	return nil
}

// AfterCommit collects the work that must run once the transaction a
// provisioning implementation was handed has committed.
type AfterCommit struct {
	mu  sync.Mutex
	fns []func(context.Context) error
}

// Add queues one closure. A nil closure is no work. The closures run in the
// order they were added.
func (a *AfterCommit) Add(after func(context.Context) error) {
	if a == nil || after == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fns = append(a.fns, after)
}

// Run runs every queued closure, once, in order, and returns their failures
// joined. Every closure runs whatever the ones before it did: one stale
// snapshot must not keep the rest of the work from running.
func (a *AfterCommit) Run(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	fns := a.fns
	a.fns = nil
	a.mu.Unlock()
	var failures []error
	for _, fn := range fns {
		if err := fn(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
