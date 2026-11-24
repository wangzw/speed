package resolve

import "github.com/vislake/speed/pkg/authn"

// Outcome says how a resolution reached its account. The zero value is no
// outcome and comes with an error.
type Outcome uint8

const (
	// OutcomeBound: the account the proof already belonged to, by an existing
	// binding or because the method named it or a phone proved it.
	OutcomeBound Outcome = iota + 1
	// OutcomeVerified: a purpose other than login confirmed the proof belongs to
	// the flow's account, and nothing was written.
	OutcomeVerified
	// OutcomeUpgraded: a binding under an earlier key of the same external identity
	// was found and the new key was bound to its account.
	OutcomeUpgraded
	// OutcomeMatched: an enterprise identity was bound to the account an
	// administrator had prepared for it.
	OutcomeMatched
	// OutcomeCreated is a first login that opened the account.
	OutcomeCreated
)

// Result is what a resolution decided.
type Result struct {
	// Account is the account the resolution landed on; empty while Pending is
	// set.
	Account authn.AccountID
	// Outcome says how the resolution reached its account.
	Outcome Outcome
	// Pending, when set, is the account a first login would open: the resolution decided to open
	// one and wrote nothing, and Create opens it once a username is chosen. Account and Outcome
	// are then empty and zero.
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
