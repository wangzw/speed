package invite

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// acceptBody is the request of an acceptance. DisplayName and Password are read
// only when the invited address has no account yet.
type acceptBody struct {
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// handleAcceptView answers GET /invitations/:token: it reads the invitation a
// token points to, so a client can show what it is about to accept. The route is
// public and the token is the whole credential.
func (p *product) handleAcceptView(c *gin.Context) {
	inv, err := p.viewToken(c.Request.Context(), c.Param("token"))
	if err != nil {
		p.answerAccept(c, "reading an invitation", err)
		return
	}
	c.JSON(200, gin.H{"invitation": invitationViewOf(inv)})
}

// handleAccept answers POST /invitations/:token: it redeems the invitation,
// making an account when the address has none and writing the membership. The
// route is public and the token is the whole credential.
func (p *product) handleAccept(c *gin.Context) {
	var body acceptBody
	if !mount.Decode(c, &body) {
		return
	}
	mem, err := p.Accept(c.Request.Context(), AcceptInvite{
		Token:       c.Param("token"),
		DisplayName: body.DisplayName,
		Password:    body.Password,
	})
	if err != nil {
		p.answerAccept(c, "accepting an invitation", err)
		return
	}
	c.JSON(201, acceptViewOf(mem))
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

// answerAccept maps a failure of the acceptance face to its response. An
// invitation that is not there is a 404; one that can no longer be accepted —
// expired, revoked or already accepted — is a 409; a bad argument is a 400; any
// other failure is a 503 whose cause stays in the log.
func (p *product) answerAccept(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, ErrInvitationNotFound):
		p.notFound(c)
	case errors.Is(err, ErrInvitationExpired), errors.Is(err, ErrInvitationRevoked),
		errors.Is(err, ErrInvitationAlreadyAccepted):
		mount.Fail(c, mount.CodeConflict)
	case errors.Is(err, authn.ErrInvalidArgument):
		p.bad(c)
	default:
		p.cause(c, op, err)
	}
}
