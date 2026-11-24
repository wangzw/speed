package selfservice

import (
	"context"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// changePassword answers PUT /password. The password module decides the policy and publishes
// authn.password.changed; the handler rotates the current session's token and revokes the
// account's other sessions, and a failed Set revokes nothing.
func (r *routes) changePassword(ctx context.Context, in changePasswordInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.authorize(c, actionPasswordChange)
	if !ok {
		return c.result()
	}
	if in.New == "" {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if err := r.deps.Passwords.Set(c.ctx, who.account, in.New, authn.SetPasswordOptions{Current: in.Current}); err != nil {
		r.fail(c, err)
		return c.result()
	}
	r.record(c.ctx, actionPasswordChange, outcomeSuccess, who.account, nil)
	if r.settle(c, who, authn.ReasonPasswordChange) {
		c.empty(http.StatusNoContent)
	}
	return c.result()
}
