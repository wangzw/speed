package authn

import "context"

// Provisioner is the hook a host implements to take part in the first login that
// creates an account, through a social provider, an enterprise IdP or an SMS
// code. It is optional and not exclusive. The login engine takes it with
// core.ResolveAll and calls it before the account is created.
//
// The host may refuse the sign-up by returning ErrProvisionRefused, which the
// client sees as an ordinary login failure; any other error is a fault of the
// seam and ends the login in a 503. Both directions refuse: nothing is created
// from half a result. The CreateAccount that comes back may change DisplayName
// and Attributes and nothing else, and the engine ignores any other field of it.
type Provisioner interface {
	// Provision is called with the account that is about to be created and the
	// way it came about, written as CreateAccount.Via writes it: jit for an
	// enterprise IdP, signup for a social or SMS first login.
	Provision(ctx context.Context, p CreateAccount, via string) (CreateAccount, error)
}
