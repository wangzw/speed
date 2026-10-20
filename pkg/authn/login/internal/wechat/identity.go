package wechat

import "github.com/vislake/speed/pkg/authn"

// The naming of a WeChat identity. A person is the same across every WeChat
// application they authorize through their union id; the open id alone is
// scoped to one application. An identity that carries only an open id is
// therefore bound under a namespace that carries the application identifier, so
// that two applications do not collide, and an identity that later gains a union
// id hands the old key over as an alias.
const (
	// providerName is the provider a WeChat identity is bound under when it
	// carries a union id.
	providerName = "wechat"
	// openIDNamespace starts the provider namespace an open id is bound under
	// when there is no union id; the application identifier follows it.
	openIDNamespace = "wechat-openid:"
	// displayName is the name shown for a WeChat identity. WeChat's nickname
	// is not an authorization fact and this client does not call the endpoint
	// that would return it, so the name is fixed.
	displayName = "微信用户"
)

// Identity is the part of the provider's answer that names the person.
type Identity struct {
	// OpenID names the person inside one application.
	OpenID string
	// UnionID names the person across every application of one WeChat
	// account. It is empty for an application the person authorized only
	// through one, or one that is not bound to a WeChat account that issues
	// union ids.
	UnionID string
}

// Proof turns the identity the provider named into the external identity a login
// proves. With a union id the subject is the union id and the open id is offered
// as an alias, so a binding made under the old open-id key is upgraded in place;
// without one the subject is the open-id key. No email is synthesized: WeChat
// has none.
func (c *Client) Proof(id Identity) authn.Proof {
	openIDKey := ExternalKey(c.appID, id.OpenID)
	if id.UnionID == "" {
		return authn.Proof{
			External: &authn.External{
				Provider:    openIDKey.Provider,
				Subject:     openIDKey.Subject,
				DisplayName: displayName,
			},
			Methods: []authn.Method{authn.MethodOAuth},
		}
	}
	return authn.Proof{
		External: &authn.External{
			Provider:    providerName,
			Subject:     id.UnionID,
			DisplayName: displayName,
		},
		Methods: []authn.Method{authn.MethodOAuth},
		Aliases: []authn.ExternalLink{openIDKey},
	}
}

// ExternalKey is the binding key of an open id: the provider namespace that
// carries the application identifier, and the open id within it. It is what the
// no-union-id subject is and what a union-id subject offers as its alias.
func ExternalKey(appID, openID string) authn.ExternalLink {
	return authn.ExternalLink{Provider: openIDNamespace + appID, Subject: openID}
}

// BindingProviders are the provider values, or the namespace prefixes, that a
// WeChat identity is bound under: the union-id namespace and the open-id prefix.
// A WeChat login method reports them so that a client can match a stored binding
// back to the method without knowing this naming.
func BindingProviders() []string {
	return []string{providerName, openIDNamespace}
}
