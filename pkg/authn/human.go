package authn

import (
	"context"
)

// HumanProof is what a caller presents to a human-verification check: the token
// the client obtained from the deployment's verifier, and the address the request
// came from. A provider may check the token against the address, which is why the
// address travels with it rather than being read from somewhere else.
//
// An empty Token means the caller presented nothing. It is not a proof of
// anything: a verifier that requires one refuses it with
// ErrHumanVerificationRequired, and a verifier that requires nothing ignores the
// whole value.
type HumanProof struct {
	// Token is the proof the client obtained from the provider. It is opaque to
	// the caller, which passes it through unread.
	Token string
	// RemoteIP is the address the request came from, as the surface resolved it.
	// A provider that checks the token against a request origin reads it; a
	// provider that does not ignores it.
	RemoteIP string
}

// HumanVerifier is the human-verification gate a deployment may put in front of
// every send of a verification message. It is the deployment's object, not the
// caller's: a deployment that leaves the gate off sends without asking for a
// proof, and one that turns a mechanism on requires one.
//
// The seam answers three questions about the deployment and checks one proof.
// Required says whether a proof is a condition of sending at all; Provider names
// the mechanism, which is a constant of the provider's code; SiteKey is the
// public identifier a client-side widget needs, empty when the mechanism needs
// none or the gate is off; and Verify checks the proof the caller presented.
//
// A proof is single-use. Verify consumes the token it accepts: a token that was
// accepted once is refused when it is presented again, whether the second
// presentation follows at once or later, and two requests presenting one token
// at the same time cannot both pass. The caller checks the proof before it
// spends any send budget, so a request the proof refuses costs no budget.
//
// Verify tells the three failures apart, because the outward answers differ:
//   - a missing, invalid, expired or already used proof is
//     ErrHumanVerificationRequired, which the surface answers with a 403 and a
//     problem code that names human verification;
//   - a proof longer than the provider admits is ErrHumanProofTooLong, which the
//     surface answers with a 400;
//   - a provider, or the store that keeps its tokens, that cannot be reached is
//     ErrUnavailable, which the surface answers with a 503. No send happens in
//     any of the three cases.
//
// The provider keeps the token out of logs, error text and metrics: the token is
// a credential, and a leaked one is a proof somebody else can present.
type HumanVerifier interface {
	// Required reports whether this deployment makes a proof a condition of
	// every send. It is false when the gate is off, and then Verify accepts any
	// proof, an empty one included.
	Required() bool

	// Provider names the mechanism this verifier is, as a constant of the
	// provider's code. A surface reports it so an operator can tell which
	// mechanism a deployment runs.
	Provider() string

	// SiteKey returns the public identifier a client-side widget needs to obtain
	// a proof from this provider. It is empty when the mechanism needs none — a
	// self-hosted proof of work does not — and when the gate is off.
	SiteKey() string

	// Verify checks that p is a valid, unused proof and consumes it. An empty
	// Token is a caller that presented nothing and is refused with
	// ErrHumanVerificationRequired; a provider that requires no proof is never
	// asked, and accepts one anyway. The failures are the three the interface
	// comment names.
	Verify(ctx context.Context, p HumanProof) error
}
