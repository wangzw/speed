package invite

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

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
		description, descErr := p.descriptionOf(ctx, inv.Tenant)
		if descErr != nil {
			return p.acceptAnswerReply("reading the tenant's description", descErr)
		}
		link := acceptedLinkOf(description)
		return speedhttp.JSONReply(http.StatusOK, AuthnInviteView{State: state, Invitation: &link}), nil
	}
	return speedhttp.JSONReply(http.StatusOK, AuthnInviteView{State: state}), nil
}

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
