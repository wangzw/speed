package authn

import "context"

// TenantMFARequirement answers whether any tenant an account is an active
// member of requires multi-factor authentication. It is a seam: the tenant side
// implements it and the authentication side consumes it, so that this package
// imports nothing of the tenant tree and the host is what wires the two
// together.
//
// An absent implementation is the legal assembly in which only the account's
// own flag and the deployment's switch decide. A read that fails is the seam
// being unable to answer, and it is never read as "no requirement": the policy
// requires a factor, and the login refuses to complete on a requirement it
// cannot establish.
type TenantMFARequirement interface {
	// RequiresMFA reports whether any tenant the account is an active member of
	// has its multi-factor requirement in force. The account is the one whose
	// factors and policy a login is deciding for.
	RequiresMFA(ctx context.Context, id AccountID) (bool, error)
}
