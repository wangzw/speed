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

// updateIdentifier answers PUT /identifiers/:id: it sets the two axes of one of
// the caller's identifiers and possibly its default contact. The identifier has
// to be the caller's own, whether it does not exist or belongs to another
// account, so the route discloses nothing about what another account holds.
//
// The body's members are optional and each is left as it is when absent. A
// registered address is always a contact, so contact true is accepted and
// changes nothing while contact false is refused. Sign-in may be set true only
// on an identifier whose ownership is proved, and the default contact may only
// be a proved email address. Those refusals are bad-request before the account
// module is asked. Changing sign-in or the default contact asks for the
// freshness every change of what proves the account asks for; changing contact
// alone does not. A write that would leave the account without a login name
// answers 409 login-identifier-required and one that would leave it without any
// way to sign in answers 409 last-login-method, and neither takes effect.
// Putting a proved email address or phone number into sign-in is a rotation
// point: the current session gets a new token and the others are revoked.
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
	// The freshness a change of the way in asks for is decided from the body, so
	// it is asked for here rather than in the precheck every other route uses.
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

	// The flags go first: they carry the refusals that must leave both parts of
	// the request without effect, and the default contact is no rotation point.
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
// the known namespaces has no class, and its unlinking publishes
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
