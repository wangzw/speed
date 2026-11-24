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

func (r *routes) updateIdentifier(ctx context.Context, in updateIdentifierInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.authorize(c, "")
	if !ok {
		return c.result()
	}
	if in.Contact == nil && in.SignIn == nil && in.DefaultContact == nil {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if in.SignIn != nil || (in.DefaultContact != nil && *in.DefaultContact) {
		id, found := authn.FromContext(c.ctx)
		if !found {
			c.unauthorized("")
			return c.result()
		}
		if !r.fresh(c, id, "") {
			return c.result()
		}
	}
	held, err := r.deps.Accounts.Identifiers(c.ctx, who.account)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	index := slices.IndexFunc(held, func(i authn.Identifier) bool { return i.ID == in.ID })
	if index < 0 {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	row := held[index]
	if row.Kind != authn.IdentifierEmail && row.Kind != authn.IdentifierPhone {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	proved := authn.Verification{Via: row.VerifiedVia}.Proves()
	if in.SignIn != nil && *in.SignIn && !proved {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if in.DefaultContact != nil && *in.DefaultContact && (row.Kind != authn.IdentifierEmail || !proved) {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}

	selected := in.SignIn != nil && *in.SignIn && !row.SignIn
	if in.Contact != nil || in.SignIn != nil {
		flags := authn.IdentifierFlags{Contact: row.Contact, SignIn: row.SignIn}
		if in.Contact != nil {
			flags.Contact = *in.Contact
		}
		if in.SignIn != nil {
			flags.SignIn = *in.SignIn
		}
		if err := r.deps.Admin.SetIdentifierFlags(c.ctx, who.account, in.ID, flags); err != nil {
			r.fail(c, err)
			return c.result()
		}
	}
	if in.DefaultContact != nil && *in.DefaultContact {
		if err := r.deps.Admin.SetDefaultContact(c.ctx, who.account, in.ID); err != nil {
			r.fail(c, err)
			return c.result()
		}
	}
	if selected && !r.settle(c, who, authn.ReasonSelf) {
		return c.result()
	}
	c.empty(http.StatusNoContent)
	return c.result()
}

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
