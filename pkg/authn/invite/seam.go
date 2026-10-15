package invite

import (
	"context"
	"errors"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Invites is the invitation seam: it holds the state machine of an invitation
// and decides whether one can be issued and whether a token can be redeemed.
//
// An invitation is a pending grant of a membership. It names an address and a
// tenant, and until it is accepted no account and no membership row exists. The
// token is the whole credential of acceptance: it is handed out once, at issue,
// and only its SHA-256 is kept, so revoking is a change of state and not the
// revocation of a signature.
//
// The design places this interface and the value types below beside the other
// authentication seams in the root package. They live here instead because the
// root package already declares Issued (the session type, an alias of the
// model's), which an invitation's Issued would collide with; a change of file
// elsewhere is the alternative. Nothing in the method set or the value types
// depends on the choice of package.
type Invites interface {
	// Issue issues one invitation. A pending invitation for the same tenant and
	// address is superseded, and its token is void from then on. The token in
	// the result is handed out here once and never again.
	Issue(ctx context.Context, in IssueInvite) (Issued, error)
	// List lists the invitations of a tenant, one page at a time. The second
	// result is the cursor of the next page, empty when there is none.
	List(ctx context.Context, tenant string, page authn.Page) ([]Invitation, string, error)
	// Revoke revokes an invitation, voiding its token. Revoking one that was
	// already accepted is ErrInvitationAlreadyAccepted; revoking one that was
	// already revoked is an idempotent success.
	Revoke(ctx context.Context, tenant, id string) error
	// Accept redeems an invitation with its token: it makes an account when the
	// address has none, and writes the membership, whose source is invite.
	Accept(ctx context.Context, in AcceptInvite) (authn.Membership, error)
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
	ID         string
	Tenant     string
	Email      string // the normalized address
	Status     InvitationStatus
	InvitedBy  authn.AccountID
	ExpiresAt  time.Time
	AcceptedAt time.Time // the zero time: not accepted
	CreatedAt  time.Time
}

// AcceptInvite is what accepting an invitation takes: the token, and the
// display name and password to create an account with when the address has
// none. The password is read only when an account is created; it is ignored
// when the address is already an account.
type AcceptInvite struct {
	Token       string
	DisplayName string
	Password    string
}

// InvitationStatus is the closed set of an invitation's states. The zero value
// pending agrees with the stored column. An expired pending invitation is still
// InvitationPending: expiry is read from the clock and ExpiresAt and is not a
// stored state.
type InvitationStatus uint8

// The stored states of an invitation.
const (
	InvitationPending  InvitationStatus = iota // pending (the zero value)
	InvitationAccepted                         // accepted
	InvitationRevoked                          // revoked
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

// The failures this module judges for itself. A caller tells them apart with
// errors.Is, and every wrap on the way out uses %w, so the chain stays whole.
// The error text never carries an address or a token.
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
	// concurrent issuance of the same tenant and address.
	ErrInvitationPending = errors.New("authn.invite: a pending invitation already exists")
	// ErrInvalidEmail reports an invited address that is not well formed.
	ErrInvalidEmail = errors.New("authn.invite: the invited address is not well formed")
	// ErrInvite reports a construction failure of this module of its own, such
	// as a failed token entropy source.
	ErrInvite = errors.New("authn.invite: the invitation could not be issued")
)
