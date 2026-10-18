package authn

import "context"

// Impersonation is the module that lets an administrator act as another account
// inside one tenant, with every step recorded. The impersonation module delivers
// it exclusively; a host may supply its own implementation that keeps to the
// promises below.
//
// Its stability is a promise to the session and token modules, the
// administration routes and the host: the method set, the fields of
// ImpersonationGrant and the closed set of end reasons. Active in particular is
// what a verifying backend relies on to check the bound session, the actor, the
// target and the membership together, and it does not repeat any of them.
type Impersonation interface {
	// Start begins an impersonation on actor's session. It checks every
	// structural restriction, asks the Decider, writes the mandatory audit row
	// and writes the record. A session has at most one unended record. It does
	// not attach the carrier, issue a token or publish the started event: it has
	// no response writer, so the handler does all three once Start returns.
	Start(ctx context.Context, actor Identity, r ImpersonationRequest) (ImpersonationGrant, error)
	// End ends the unended record on actor's session, with the reason explicit.
	// actor may be the impersonating identity or the starter's own. A session with
	// none is ErrNotImpersonating. It changes the record, writes the audit row and
	// publishes the event, and leaves the carrier alone. Ending is a move in the
	// safe direction, so a failed audit write does not stop it. reason goes into
	// the audit row only.
	End(ctx context.Context, actor Identity, reason string) error
	// Revoke ends record id by force, with the reason revoked; by is who revoked.
	// Before it writes revoked it judges the record by the validity conditions and
	// writes a lapse back, so a record that has expired, lost its bound session,
	// lost its actor or target, or lost its membership ends with its own reason and
	// the revoke is a no-op: no revoke audit row and no revoke event is written.
	// Only a record that is still valid after the judgement is written revoked.
	// It is idempotent: a record that has ended is a no-op, and a record that is
	// not there is ErrNotImpersonating. It asks no Decider, because the
	// administration handler has already authorized the call. The judgement reads
	// the bound session from the store and not through the check-ttl cache. A
	// dependency read that cannot answer is ErrUnavailable, and the record stays
	// unended.
	Revoke(ctx context.Context, id string, by Identity, reason string) error
	// Active says whether record id is valid now. A record that is not there, has
	// ended or has lapsed is false in the second result, and a lapse read from a
	// record that has not ended is written back. With an error the caller refuses
	// and does not read the record as valid.
	Active(ctx context.Context, id string) (ImpersonationGrant, bool, error)
	// List returns one page of the records the query selects, keyed by cursor: the
	// second result is the cursor of the next page, empty when there is none.
	// Before it returns, it judges every unended record on the page by the validity
	// conditions and writes a lapse back, and the page it returns is the page as
	// persisted. It therefore has write side effects, and each lapse it writes
	// counts an end and may publish an ended event. The judgement reads the
	// dependency state from the store and not through the check-ttl cache. A
	// dependency read that cannot answer fails the whole page with ErrUnavailable;
	// no partial page is returned. Records are never deleted, so a query by target
	// is the complete answer to who impersonated whom.
	List(ctx context.Context, q GrantQuery) ([]ImpersonationGrant, string, error)
}
