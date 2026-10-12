package sso

import (
	"context"
	"errors"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// RememberAssertion records a consumed SAML assertion's identifier on the replay
// line and reports a repeat as authn.ErrAssertionReplay. It is the operation the
// SAML method reaches through the register value rather than through
// SSOProviders: the interface's method set is fixed, so the SAML method asserts
// the register to its own narrow interface, and the register answers it or the
// SAML module refuses to build. not_on_or_after is the earlier of the assertion's
// and its bearer confirmation's condition.
func (p *product) RememberAssertion(ctx context.Context, idpID, assertionID string, notOnOrAfter time.Time) error {
	if idpID == "" || assertionID == "" {
		return errors.New("authn.sso: an assertion identifier needs a provider and an identifier")
	}
	err := p.register.rememberAssertion(ctx, idpID, assertionID, notOnOrAfter, p.now())
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authn.ErrAssertionReplay):
		return authn.ErrAssertionReplay
	default:
		return p.unavailable(err)
	}
}
