// Package provisioning declares the in-transaction seams an account needs
// other modules to satisfy.
//
// An account is the anchor of data other capabilities hold: its tenant
// memberships, the roles bound to it and, for a user account, the organization
// that belongs to it alone. Some of that data has to exist for exactly as long
// as the account does, and a failure to write it must undo the account itself.
// AccountLifecycle is not that seam: its hooks run after the account's
// transaction has committed, so a hook that fails leaves the account in being
// without the data the account is not meant to exist without.
//
// AccountProvisioning is the seam that runs inside the account's transaction
// instead. The account module calls ProvisionAccount after the account row, its
// identifiers and its credential are written and before the creation event is
// staged, and ProvisionAccountDeletion after the account's own rows are erased
// and before the transaction returns. An implementation writes through the
// *gorm.DB it is handed and nothing else: that handle is the account module's
// transaction, so a row written on a connection of its own would survive a
// rollback the account does not survive. A provisioning failure fails the
// account's transaction, and the account is not created or not deleted.
//
// MembershipWriter is the account module's transaction-aware membership write.
// The account module delivers it, and a provisioning implementation that writes
// the creator's membership in the same transaction takes it from the registry.
// The plain Memberships surface opens a transaction of its own and is therefore
// the wrong path there.
//
// AfterCommit is how the work a transaction-aware write owes after the commit —
// the roster or policy snapshot reread, and the event that tells the other
// replicas — reaches the account module's commit boundary. The account module
// puts a collector on the context it hands a provisioning implementation, and
// runs it once the transaction has committed; an implementation hands the
// resulting closure of every transaction-aware write to AfterCommitOf(ctx).
package provisioning

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// AccountFacts is what the account module knew of the account when it called a
// provisioning method. ID, Kind and Status are as the account row carries them;
// Username is the normalized username identifier, empty for a service account;
// and DisplayName is the name the account was created with, before a deletion
// erases it.
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

// AccountProvisioning is the seam that writes what an account owns inside the
// transaction that creates or deletes the account. The account module requires
// one implementation; an assembly without it does not start.
//
// Every statement of an implementation runs on the tx it is handed. Opening a
// connection of its own would write outside the account's transaction, so the
// data would outlive a rollback and the account would not, or a committed
// account would be left without the data it was supposed to own. A non-nil
// error fails the account's transaction and is returned to the account
// module's caller.
type AccountProvisioning interface {
	// ProvisionAccount writes what a user account owns for the whole of its
	// life. It is called after the account row, its identifiers and its
	// credential are written and before the creation event is staged, and it
	// must write nothing for a service account.
	ProvisionAccount(ctx context.Context, tx *gorm.DB, facts AccountFacts) error
	// ProvisionAccountDeletion ends what ProvisionAccount wrote, keeping the
	// parts an identifier's history needs. It is called after the account's
	// own rows are erased, inside the same transaction, and must be idempotent.
	ProvisionAccountDeletion(ctx context.Context, tx *gorm.DB, facts AccountFacts) error
}

// MembershipWriter is the account module's transaction-aware membership
// surface: the writes that join the caller's transaction, and the read that
// holds the account's row in it. The account module delivers it exclusively.
//
// The two write methods write in the transaction they are handed, unlike the
// Memberships surface, whose Put and Remove open a transaction of their own.
// Each returns a closure for the work that belongs after the caller's commit —
// publishing the membership events, staged in the transaction — and nil when
// the call changed nothing. The caller runs the closure once its transaction has
// committed.
//
// MembershipTx reads one membership under the account's row lock, so a caller
// that has to act on the answer (a transfer checking the new owner is an active
// member) cannot have a concurrent suspension change it before the caller's
// commit: the suspension's own write takes that row first.
//
// Neither write method asks a MembershipGuard or calls a MembershipLifecycle
// hook: a guard protects an explicit removal from a tenant that stays in
// service, and a hook cleans up after a removal that has committed. A call
// inside a transaction is neither, and the caller owns the decision to ask one.
type MembershipWriter interface {
	// MembershipTx reads one membership in the transaction it is handed,
	// taking the account's row first. The second result is false when the
	// account holds no membership in the tenant, and also when the account
	// does not exist.
	MembershipTx(ctx context.Context, tx *gorm.DB, tenant string, account authn.AccountID) (authn.Membership, bool, error)
	// PutMembershipTx writes a membership, and writing the same one again is no
	// change. It is Put's rules without the transaction Put opens.
	PutMembershipTx(ctx context.Context, tx *gorm.DB, m authn.Membership) (after func(context.Context) error, err error)
	// RemoveMembershipTx deletes a membership. One that is not there is already
	// removed and returns a nil closure. It is RemoveMembershipForDeletion's
	// rules without the transaction that method opens.
	RemoveMembershipTx(ctx context.Context, tx *gorm.DB, account authn.AccountID, tenant string) (after func(context.Context) error, err error)
	// RemoveTenantMembershipsTx deletes every membership of one tenant in the
	// transaction it is handed, publishing authn.membership.changed for each row
	// it removed. It is the organization-deletion path: the tenant is becoming a
	// tombstone, so no guard is asked and no lifecycle hook is called. A tenant
	// with no membership returns a nil closure.
	RemoveTenantMembershipsTx(ctx context.Context, tx *gorm.DB, tenant string) (after func(context.Context) error, err error)
}

// afterCommitKey is the context key of an AfterCommit collector.
type afterCommitKey struct{}

// WithAfterCommit returns a context carrying a fresh collector, and the
// collector. The caller runs it once the transaction it covers has committed.
func WithAfterCommit(ctx context.Context) (context.Context, *AfterCommit) {
	a := &AfterCommit{}
	return context.WithValue(ctx, afterCommitKey{}, a), a
}

// AfterCommitOf returns the collector a context carries, if any. It is nil for
// a context that carries none, and a caller of Add need not distinguish: a nil
// collector drops its work, which is what an implementation called outside the
// account module's transaction expects.
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
