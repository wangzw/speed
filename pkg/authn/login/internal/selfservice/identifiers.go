package selfservice

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// removeIdentifier answers DELETE /identifiers/:id. The identifier has to be one
// of the caller's own: an identifier that is not, whether it does not exist or
// belongs to another account, is answered the same way, so that a caller learns
// nothing about identifiers it does not hold. Whether the removal leaves the
// account a way to log in is the account module's to say.
func (r *routes) removeIdentifier(ctx context.Context, in identifierPathInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.authorize(c, "")
	if !ok {
		return c.result()
	}
	held, err := r.deps.Accounts.Identifiers(c.ctx, who.account)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	if !slices.ContainsFunc(held, func(i authn.Identifier) bool { return i.ID == in.ID }) {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if err := r.deps.Admin.RemoveIdentifier(c.ctx, who.account, in.ID); err != nil {
		r.fail(c, err)
		return c.result()
	}
	c.empty(http.StatusNoContent)
	return c.result()
}

// unlink answers DELETE /links/:id. As for an identifier, the binding has to be
// one of the caller's own. The account module refuses to remove the last way to
// log in (ErrLastLoginMethod, a 409), and counts a way that only an IdP declared
// as none, which is why the freshness this route asks for is a local one. After
// the unbinding the current session is rotated, the others are revoked and
// authn.binding.changed is published.
func (r *routes) unlink(ctx context.Context, in linkPathInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.authorize(c, actionBindingUnlink)
	if !ok {
		return c.result()
	}
	held, err := r.deps.Accounts.Externals(c.ctx, who.account)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	i := slices.IndexFunc(held, func(b authn.ExternalBinding) bool { return b.ID == in.ID })
	if i < 0 {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	provider := held[i].Provider
	if err := r.deps.Admin.UnlinkExternal(c.ctx, who.account, in.ID); err != nil {
		r.fail(c, err)
		return c.result()
	}
	class, known := providerClass(provider)
	var detail map[string]string
	if !known {
		log.FromContext(c.ctx).Warn(moduleName + ": an external identity of a provider outside the classes of the " +
			"event payload was unlinked, so no authn.binding.changed was published")
	} else {
		detail = map[string]string{"provider": class}
		r.publish(c.ctx, authn.EventBindingChanged, authn.BindingChanged{
			AccountID:     string(who.account),
			ProviderClass: class,
			Change:        "unlinked",
			Source:        "explicit",
		})
	}
	r.record(c.ctx, actionBindingUnlink, outcomeSuccess, who.account, detail)
	if r.settle(c, who, authn.ReasonSelf) {
		c.empty(http.StatusNoContent)
	}
	return c.result()
}

// providerClass is the class of a provider in the closed set the event payload
// carries: google, github, wechat, oidc or saml. A provider written in none of
// the namespaces of the account design has no class, and its unlinking publishes
// no event rather than one with a value outside the set.
func providerClass(provider string) (string, bool) {
	switch {
	case provider == "google", provider == "github", provider == "wechat":
		return provider, true
	case strings.HasPrefix(provider, "wechat-openid:"):
		return "wechat", true
	case strings.HasPrefix(provider, "oidc:"):
		return "oidc", true
	case strings.HasPrefix(provider, "saml:"):
		return "saml", true
	}
	return "", false
}
