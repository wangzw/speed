package invite

import (
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The views the routes answer with. Each is built field by field from a value
// type so that a field added later does not reach a client by being there. No
// view carries a token except the one issue answer, which hands it out once.

// invitationView is one invitation as the management routes show it. It never
// carries the token.
type invitationView struct {
	ID         string    `json:"id"`
	Tenant     string    `json:"tenant"`
	Email      string    `json:"email"`
	Status     string    `json:"status"`
	InvitedBy  string    `json:"invited_by"`
	ExpiresAt  time.Time `json:"expires_at"`
	AcceptedAt time.Time `json:"accepted_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func invitationViewOf(v Invitation) invitationView {
	return invitationView{
		ID:         v.ID,
		Tenant:     v.Tenant,
		Email:      v.Email,
		Status:     v.Status.String(),
		InvitedBy:  string(v.InvitedBy),
		ExpiresAt:  v.ExpiresAt.UTC(),
		AcceptedAt: v.AcceptedAt.UTC(),
		CreatedAt:  v.CreatedAt.UTC(),
	}
}

// issuedView is the answer of an issue: the invitation's view and the acceptance
// token, handed out here once. The token must not be logged.
type issuedView struct {
	Invitation invitationView `json:"invitation"`
	Token      string         `json:"token"`
}

// acceptView is the answer of an acceptance: the membership that was written.
type acceptView struct {
	Account   string `json:"account"`
	Tenant    string `json:"tenant"`
	Active    bool   `json:"active"`
	Source    string `json:"source"`
	SSOExempt bool   `json:"sso_exempt"`
}

func acceptViewOf(mem authn.Membership) acceptView {
	return acceptView{
		Account:   string(mem.Account),
		Tenant:    mem.Tenant,
		Active:    mem.Active,
		Source:    mem.Source,
		SSOExempt: mem.SSOExempt,
	}
}
