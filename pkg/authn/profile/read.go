package profile

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
)

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
		// A read failure is never answered as none.
		return AuthnProfileProfile{}, p.cause("reading the signup state", err)
	}
	return profileViewOf(account, identifiers, links, signup), nil
}
