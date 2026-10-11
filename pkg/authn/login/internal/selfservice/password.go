package selfservice

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// passwordRequest is the body of PUT /password. The current password is
// optional: an account that signed up through a provider has none.
type passwordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

// changePassword answers PUT /password. The password module decides the policy
// and publishes authn.password.changed, so the handler writes neither the check
// nor the event. What it adds is what Passwords.Set leaves to its caller: on
// success it rotates the current session's token and revokes the account's other
// sessions, and a failed Set revokes nothing.
func (r *routes) changePassword(c *gin.Context) {
	who, ok := r.authorize(c, actionPasswordChange)
	if !ok {
		return
	}
	var req passwordRequest
	if !mount.Decode(c, &req) {
		return
	}
	if req.New == "" {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	ctx := c.Request.Context()
	if err := r.deps.Passwords.Set(ctx, who.account, req.New, authn.SetPasswordOptions{Current: req.Current}); err != nil {
		r.fail(c, err)
		return
	}
	r.record(ctx, actionPasswordChange, outcomeSuccess, who.account, nil)
	if r.settle(c, who, authn.ReasonPasswordChange) {
		c.Status(http.StatusNoContent)
	}
}
