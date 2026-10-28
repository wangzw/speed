package resolve

import (
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

// Outcome says how a resolution reached its account. The zero value is no
// outcome and comes with an error.
type Outcome uint8

// The outcomes.
const (
	// OutcomeBound: the account the proof already belonged to, by an existing
	// binding or because the method named it or a phone proved it.
	OutcomeBound Outcome = iota + 1
	// OutcomeVerified: a purpose other than login confirmed the proof belongs to
	// the flow's account, and nothing was written.
	OutcomeVerified
	// OutcomeUpgraded: a binding under an earlier key of the same external identity
	// was found and the new key was bound to its account (A8).
	OutcomeUpgraded
	// OutcomeMatched: an enterprise identity was bound to the account an
	// administrator had prepared for it.
	OutcomeMatched
	// OutcomeMerged: the external identity was bound to an account that existed
	// before, by the automatic link of A4 or the merge of A5. The result carries
	// the obligations of A10.
	OutcomeMerged
	// OutcomeCreated: the login created the account.
	OutcomeCreated
)

// Cleanup is something the caller must do before it creates the session of a
// login that merged into an existing account.
type Cleanup uint8

// The cleanups of A10.
const (
	// CleanupRevokeSessions: revoke every session the account had, all of them
	// and none excepted.
	CleanupRevokeSessions Cleanup = iota + 1
	// CleanupClearPassword: delete the account's password credential, through
	// authn.Passwords.Clear.
	CleanupClearPassword
)

// Result is what a resolution decided.
type Result struct {
	Account authn.AccountID
	Outcome Outcome
	// Pending, when it is set, is the account a first login would open: the
	// resolution decided to open one and wrote nothing, and Create opens it
	// once the person has chosen a username. Account and Outcome are then empty
	// and zero: no account exists yet.
	Pending *Pending
	// Cleanup is what the caller does before the session of this login is
	// created, in the order listed. It is empty unless the outcome is
	// OutcomeMerged. A session created first would be revoked with the others, and
	// a password deleted before the sessions are revoked leaves a window in which
	// the squatter's session outlives the merge.
	Cleanup []Cleanup
	// MembershipCreated says the login created the account's membership of the
	// provider's tenant.
	MembershipCreated bool
	// RefreshErr is the failure of refreshing the display fields of an existing
	// binding. It is not a failure of the login, which does not depend on those
	// fields, and is for the caller's log.
	RefreshErr error
}

// Clone returns a copy that shares no slice or map with r.
func (r Result) Clone() Result {
	r.Cleanup = slices.Clone(r.Cleanup)
	if r.Pending != nil {
		pending := r.Pending.Clone()
		r.Pending = &pending
	}
	return r
}

// mergeCleanup is the cleanup of A10 in its order.
func mergeCleanup() []Cleanup {
	return []Cleanup{CleanupRevokeSessions, CleanupClearPassword}
}
