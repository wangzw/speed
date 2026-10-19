package invite

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// handleAcceptView answers GET /invitations/:token: it reads the invitation a
// token points to, so a client can show what it is about to accept. The route is
// public and the token is the whole credential. An unknown token is a bare 404,
// which the handler builds.
func (p *product) handleAcceptView(ctx context.Context, in acceptViewInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	inv, err := p.viewToken(ctx, in.Token)
	if err != nil {
		return p.acceptAnswerReply("reading an invitation", err)
	}
	return speedhttp.JSONReply(http.StatusOK, AuthnInviteView{Invitation: invitationViewOf(inv)}), nil
}

// handleAccept answers POST /invitations/:token: it redeems the invitation,
// making an account when the address has none and writing the membership. The
// route is public and the token is the whole credential.
//
// A request that declares a length of zero carries no body and is refused: the
// route has always required a body, while a body of "{}" is a body and is
// accepted. An unknown length — a chunked request, or an HTTP/2 one without
// Content-Length — says nothing about whether a body is there, so the request is
// bound normally and the binder is what reads the body.
func (p *product) handleAccept(ctx context.Context, in acceptInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	if meta.ContentLength == 0 {
		return nil, p.badRequest()
	}
	mem, err := p.Accept(ctx, AcceptInvite{
		Token:       in.Token,
		DisplayName: in.DisplayName,
		Password:    in.Password,
	})
	if err != nil {
		return p.acceptAnswerReply("accepting an invitation", err)
	}
	return speedhttp.JSONReply(http.StatusCreated, acceptViewOf(mem)), nil
}

// viewToken reads the invitation a token points to. It is the read side of the
// acceptance face, which is not part of the Invites capability.
func (p *product) viewToken(ctx context.Context, token string) (Invitation, error) {
	if token == "" {
		return Invitation{}, ErrInvitationNotFound
	}
	row, found, err := p.findByDigest(p.conn(ctx), tokenDigest(token))
	if err != nil {
		return Invitation{}, err
	}
	if !found {
		return Invitation{}, ErrInvitationNotFound
	}
	return toInvitation(row), nil
}
