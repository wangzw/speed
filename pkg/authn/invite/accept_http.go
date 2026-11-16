package invite

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// handleAcceptView answers GET /api/v1/auth/invitations/:token: it reads the invitation a
// token points to, so a client can show what it is about to accept. The route is
// public and the token is the whole credential. An unknown token is a bare 404,
// which the handler builds.
//
// The probe budget is charged before the token is looked up, so a source that
// presents many tokens is throttled whether or not any of them exists: a 429
// here says the window is spent, never that a token was found.
func (p *product) handleAcceptView(ctx context.Context, in acceptViewInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	if err := p.chargeProbe(ctx, in.Token, meta.ClientIP, p.at()); err != nil {
		return p.acceptAnswerReply("probing an invitation", err)
	}
	inv, state, err := p.viewToken(ctx, in.Token)
	if err != nil {
		return p.acceptAnswerReply("reading an invitation", err)
	}
	switch state {
	case viewStatePending:
		description, descErr := p.descriptionOf(ctx, inv.Tenant)
		if descErr != nil {
			return p.acceptAnswerReply("reading the tenant's description", descErr)
		}
		inviter, inviterErr := p.inviterNameOf(ctx, inv.InvitedBy)
		if inviterErr != nil {
			return p.acceptAnswerReply("reading the inviter's name", inviterErr)
		}
		link := pendingLinkOf(inv, description, inviter)
		return speedhttp.JSONReply(http.StatusOK, AuthnInviteView{State: state, Invitation: &link}), nil
	case viewStateAccepted:
		// An accepted link names the organization the caller may now belong to,
		// so the client can offer it; it shows neither the address nor the
		// inviter.
		description, descErr := p.descriptionOf(ctx, inv.Tenant)
		if descErr != nil {
			return p.acceptAnswerReply("reading the tenant's description", descErr)
		}
		link := acceptedLinkOf(description)
		return speedhttp.JSONReply(http.StatusOK, AuthnInviteView{State: state, Invitation: &link}), nil
	}
	// An expired or revoked token answers its state alone: the address, the
	// organization and the deadline are not shown once the link grants nothing.
	return speedhttp.JSONReply(http.StatusOK, AuthnInviteView{State: state}), nil
}

// handleAccept answers POST /api/v1/auth/invitations/:token: it redeems the invitation and
// writes the membership for the authenticated caller. The route is optional
// rather than public, so a request that carries a credential gets its identity;
// the handler refuses one that carries none, because the token is not proof of
// anything on its own — the inviter holds it too.
//
// A request that declares a length of zero carries no body and is refused: the
// route has always required a body, while a body of "{}" is a body and is
// accepted. An unknown length — a chunked request, or an HTTP/2 one without
// Content-Length — says nothing about whether a body is there, so the request is
// bound normally and the binder is what reads the body.
func (p *product) handleAccept(ctx context.Context, in acceptInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	if _, err := p.caller(ctx); err != nil {
		return nil, err
	}
	if err := p.chargeProbe(ctx, in.Token, meta.ClientIP, p.at()); err != nil {
		return p.acceptAnswerReply("probing an invitation", err)
	}
	if meta.ContentLength == 0 {
		return nil, p.badRequest()
	}
	accepted, err := p.Accept(ctx, AcceptInvite{Token: in.Token})
	if err != nil {
		return p.acceptAnswerReply("accepting an invitation", err)
	}
	tenantName, nameErr := p.nameOf(ctx, accepted.Tenant)
	if nameErr != nil {
		return p.acceptAnswerReply("reading the tenant's name", nameErr)
	}
	return speedhttp.JSONReply(http.StatusCreated, acceptViewOf(accepted, tenantName)), nil
}

// viewToken reads the invitation a token points to and the state the read
// judges: pending while the link is redeemable, and expired, accepted or
// revoked once it is not. It answers the whole row view whatever the state; the
// handlers decide field by field what a given state may publish, so a state
// that must not show the address is enforced by the view built for it rather
// than by dropping data here. It is the read side of the acceptance face, which
// is not part of the Invites capability.
func (p *product) viewToken(ctx context.Context, token string) (Invitation, string, error) {
	if token == "" {
		return Invitation{}, "", ErrInvitationNotFound
	}
	row, found, err := p.findByDigest(p.conn(ctx), tokenDigest(token))
	if err != nil {
		return Invitation{}, "", err
	}
	if !found {
		return Invitation{}, "", ErrInvitationNotFound
	}
	return toInvitation(row), readState(row, p.at()), nil
}
