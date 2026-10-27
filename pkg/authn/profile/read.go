package profile

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
)

// handleGetMe answers GET B/me: the caller's own profile, read from the account
// the identity names.
//
// It is registered through mount.Impersonable, so an impersonating identity
// reaches it and reads the impersonated account the identity's Account names,
// exactly as the account's own request does. A request with no identity, or an
// identity with no account, is the 401 the gateway writes for a route that
// requires one; nothing of the client's own naming is dereferenced, because the
// account comes from the context and no path parameter carries one.
func (p *product) handleGetMe(ctx context.Context, _ struct{}) (AuthnProfileProfile, error) {
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		return AuthnProfileProfile{}, p.unauthorized()
	}
	account, err := p.accounts.Get(ctx, id.Account())
	if err != nil {
		return AuthnProfileProfile{}, p.readFailure(err)
	}
	identifiers, err := p.accounts.Identifiers(ctx, id.Account())
	if err != nil {
		return AuthnProfileProfile{}, p.readFailure(err)
	}
	links, err := p.accounts.Externals(ctx, id.Account())
	if err != nil {
		return AuthnProfileProfile{}, p.readFailure(err)
	}
	signup, err := p.signupVerification.SignupStateOf(ctx, id.Account())
	if err != nil {
		// A read failure is never answered as none: an unavailable state read
		// would otherwise report a verified account as never registered.
		return AuthnProfileProfile{}, p.cause("reading the signup state", err)
	}
	return profileViewOf(account, identifiers, links, signup), nil
}
