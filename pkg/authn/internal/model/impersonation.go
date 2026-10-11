package model

import "time"

// The reasons an impersonation record ends, as ImpersonationGrant.EndReason
// writes them. The set is closed.
const (
	EndExplicit          = "explicit"           // the impersonator ended it
	EndRevoked           = "revoked"            // an administrator revoked it
	EndExpired           = "expired"            // it ran past its end
	EndSessionEnded      = "session-ended"      // the impersonator's session was revoked or expired
	EndActorDisabled     = "actor-disabled"     // the impersonator is no longer enabled
	EndTargetDisabled    = "target-disabled"    // the target is no longer enabled
	EndMembershipRemoved = "membership-removed" // the target is no longer a member of the tenant
	EndStartFailed       = "start-failed"       // attaching the carrier failed after the record was written
)

// ValidEndReason reports whether s is one of the End constants above.
func ValidEndReason(s string) bool {
	switch s {
	case EndExplicit, EndRevoked, EndExpired, EndSessionEnded, EndActorDisabled,
		EndTargetDisabled, EndMembershipRemoved, EndStartFailed:
		return true
	}
	return false
}

// ImpersonationRequest is what starting an impersonation takes.
type ImpersonationRequest struct {
	Target AccountID
	Tenant string        // the tenant the impersonation is confined to; required
	Reason string        // 1 to 500 characters, no control character
	TTL    time.Duration // zero: the setting
}

// ImpersonationGrant is one impersonation record.
type ImpersonationGrant struct {
	ID        string
	Actor     AccountID
	Target    AccountID
	Tenant    string
	Reason    string // only in the audit row and the record; never in an event
	SessionID SessionID
	StartedAt time.Time
	EndsAt    time.Time
	EndedAt   time.Time // the zero time: not ended
	EndedBy   string
	EndReason string // one of the End constants
}

// GrantQuery selects impersonation records for Impersonation.List. The zero
// value selects every record, the first page of the default size.
type GrantQuery struct {
	Actor, Target AccountID
	After         string
	Limit         int // see Page.EffectiveLimit
}

// EffectiveLimit is the page size the query asks for, read as Page's is.
func (q GrantQuery) EffectiveLimit() int { return pageLimit(q.Limit) }
