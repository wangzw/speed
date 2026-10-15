package profile

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// handleGetMe answers GET B/me: the caller's own profile, read from the account
// the identity names.
//
// It is the one route an impersonating identity may reach, registered through
// mount.Impersonable, and it then reads the impersonated account the identity's
// Account names. A request with no identity, or an identity with no account, is
// the 401 the gateway writes for a route that requires one; nothing of the
// client's own naming is dereferenced, because the account comes from the
// context and no path parameter carries one.
func (p *product) handleGetMe(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		mount.Unauthorized(c, p.mounter.Endpoint(), "")
		return
	}
	account, err := p.accounts.Get(ctx, id.Account())
	if err != nil {
		p.readFailure(c, err)
		return
	}
	identifiers, err := p.accounts.Identifiers(ctx, id.Account())
	if err != nil {
		p.readFailure(c, err)
		return
	}
	links, err := p.accounts.Externals(ctx, id.Account())
	if err != nil {
		p.readFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, profileViewOf(account, identifiers, links))
}
