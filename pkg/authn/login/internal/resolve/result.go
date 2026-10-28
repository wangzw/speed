package resolve

import "github.com/vislake/speed/pkg/authn"

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
	// OutcomeCreated: the login created the account.
	OutcomeCreated
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
	if r.Pending != nil {
		pending := r.Pending.Clone()
		r.Pending = &pending
	}
	return r
}
