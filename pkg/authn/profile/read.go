package profile

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
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
	return profileViewOf(account, identifiers, links), nil
}
