package invite

import (
	"context"
	"errors"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Invites is the invitation seam: it holds the state machine and decides
// whether an invitation can be issued and whether a token can be redeemed.
type Invites interface {
	// Issue issues one invitation. An unexpired pending invitation for the same
	// tenant and address is refused; an expired one is replaced. The token in the
	// result is handed out here once and never again.
	Issue(ctx context.Context, in IssueInvite) (Issued, error)
	// List lists the invitations of a tenant, one page at a time. The second
	// result is the cursor of the next page, empty when there is none.
	List(ctx context.Context, tenant string, page authn.Page) ([]Invitation, string, error)
	// Revoke revokes an invitation, voiding its token. Revoking one that was
	// already accepted is ErrInvitationAlreadyAccepted; revoking one that was
	// already revoked is an idempotent success.
	Revoke(ctx context.Context, tenant, id string) error
	// Accept redeems an invitation for an authenticated caller who holds the
	// invited address as a proven email identifier. It writes the membership and
	// the invitation's role in one transaction and never creates an account.
	Accept(ctx context.Context, in AcceptInvite) (Accepted, error)
}

// IssueInvite is what issuing an invitation takes.
type IssueInvite struct {
	// Tenant is the string form of a tenant identifier.
	Tenant string
	// Email is the invited address as it was given, before normalization.
	Email string
	// ExpiresAt is the absolute deadline. The zero time takes the configured
	// lifetime instead.
	ExpiresAt time.Time
	// Role is the role the invitation grants beside the membership. The empty
	// string grants no role; a non-empty role is checked against the caller's
	// permissions before any row is written.
	Role string
}

// Accepted is what one acceptance wrote: the membership and the role that was
// bound beside it. The membership embeds the capability's Membership, so a
// caller reads its fields as it always did.
type Accepted struct {
	authn.Membership
	// Role is the role the acceptance bound to the account in the tenant. It is
	// empty when the invitation carried no role.
	Role string
}

// Issued is one issue's result: the invitation's view and the one-time token.
type Issued struct {
	Invitation Invitation
	// Token is the acceptance token. It is handed out once and must not reach a
	// log, an event or a trace attribute.
	Token string
}

// Invitation is the read-only view of one invitation. It never carries the
// token.
type Invitation struct {
	ID        string
	Tenant    string
	Email     string // the normalized address
	Status    InvitationStatus
	InvitedBy authn.AccountID
	// Role is the role the invitation grants beside the membership. The empty
	// string grants no role.
	Role       string
	ExpiresAt  time.Time
	AcceptedAt *time.Time // nil: not accepted
	RevokedAt  *time.Time // nil: not revoked
	LastSentAt time.Time  // the most recent send; issuance always writes it
	SendCount  int        // the sends so far; the first issuance counts as one
	// SupersededAt is when a later invitation took this row's live slot. The read
	// side judges a superseded pending invitation expired; no view publishes it.
	SupersededAt *time.Time
	CreatedAt    time.Time
}

// AcceptInvite is what accepting an invitation takes. Only the token is read;
// the account-opening fields are ignored and stay so an older client can keep
// sending them.
type AcceptInvite struct {
	Token       string
	Username    string // ignored: acceptance never opens an account
	DisplayName string // ignored: acceptance never opens an account
	Password    string // ignored: acceptance never opens an account
}

// InvitationStatus is the closed set of an invitation's stored states. An
// expired pending invitation is still InvitationPending: expiry is read from the
// clock and ExpiresAt and is not stored.
type InvitationStatus uint8

// InvitationPending, InvitationAccepted and InvitationRevoked are the stored
// states of an invitation.
const (
	InvitationPending InvitationStatus = iota
	InvitationAccepted
	InvitationRevoked
)

// Valid reports whether s is one of the stored states.
func (s InvitationStatus) Valid() bool { return s <= InvitationRevoked }

// String is the status's name. A value outside the set reads unknown.
func (s InvitationStatus) String() string {
	switch s {
	case InvitationPending:
		return "pending"
	case InvitationAccepted:
		return "accepted"
	case InvitationRevoked:
		return "revoked"
	}
	return "unknown"
}

// The failures this module judges for itself, told apart with errors.Is. The
// error text never carries an address or a token.
var (
	// ErrInvitationNotFound reports a token or an identifier that points to no
	// invitation.
	ErrInvitationNotFound = errors.New("authn.invite: invitation not found")
	// ErrInvitationExpired reports a token of a pending invitation that is past
	// its deadline.
	ErrInvitationExpired = errors.New("authn.invite: invitation expired")
	// ErrInvitationRevoked reports a token of an invitation that was revoked.
	ErrInvitationRevoked = errors.New("authn.invite: invitation revoked")
	// ErrInvitationAlreadyAccepted reports a token of an invitation that was
	// accepted, or the revocation of an accepted invitation.
	ErrInvitationAlreadyAccepted = errors.New("authn.invite: invitation already accepted")
	// ErrInvitationPending reports that issuing lost the race against a
	// concurrent issuance of the same tenant and address, or that the address
	// already holds an unexpired pending invitation.
	ErrInvitationPending = errors.New("authn.invite: a pending invitation already exists")
	// ErrInvitationMember reports that the invited address belongs to an
	// account that is already a member of the tenant, active or suspended.
	ErrInvitationMember = errors.New("authn.invite: the invited address is already a member")
	// ErrInvitationTargetInvalid reports a target an invitation may not name: a
	// service account's address, the system tenant, or an acceptance caller that
	// is not a user account.
	ErrInvitationTargetInvalid = errors.New("authn.invite: the invitation target cannot be invited")
	// ErrInvitationExpiryTooLong reports an explicit deadline later than the
	// longest an invitation may live from its issuance.
	ErrInvitationExpiryTooLong = errors.New("authn.invite: the invitation deadline is too far ahead")
	// ErrInvitationUnauthenticated reports an acceptance with no authenticated
	// caller. A caller that is authenticated but is not a user account is
	// ErrInvitationTargetInvalid instead.
	ErrInvitationUnauthenticated = errors.New("authn.invite: accepting an invitation requires an authenticated user account")
	// ErrInvitationEmailUnproved reports an authenticated caller who does not
	// hold the invited address as a proven email identifier. The client answers
	// it by showing the proof step; the invitation stays pending.
	ErrInvitationEmailUnproved = errors.New("authn.invite: the caller has not proved the invited address")
	// ErrInvitationMembershipInactive reports an acceptance by an account whose
	// membership in the tenant is suspended. The membership is not reactivated
	// silently, and the invitation is revoked.
	ErrInvitationMembershipInactive = errors.New("authn.invite: the caller's membership is suspended")
	// ErrInvitationTenantInactive reports an acceptance into a tenant that is
	// disabled. The invitation is left pending, so enabling the tenant makes the
	// same token redeemable again.
	ErrInvitationTenantInactive = errors.New("authn.invite: the tenant is not being served")
	// ErrInvitationRoleWithdrawn reports a role that can no longer be granted: the
	// role is gone, or the inviter no longer holds every permission it carries.
	// The acceptance revokes the invitation.
	ErrInvitationRoleWithdrawn = errors.New("authn.invite: the role the invitation carries can no longer be granted")
	// ErrInvalidEmail reports an invited address that is not well formed.
	ErrInvalidEmail = errors.New("authn.invite: the invited address is not well formed")
	// ErrInvite reports a construction failure of this module of its own, such
	// as a failed token entropy source.
	ErrInvite = errors.New("authn.invite: the invitation could not be issued")
)
