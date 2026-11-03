package login

import (
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// checkHumanProof holds the anonymous password reset's email branch to the
// deployment's human-verification gate. The SMS branch hands its proof to
// SMSCodes.Send, which checks and consumes it once before any budget; the email
// code does not pass through that sender, so its route checks the proof here.
//
// A deployment with no gate, and one whose gate is off, require nothing and are
// skipped entirely. The proof is checked before any send budget is spent, so a
// refused request costs none. A proof over the field's bound is a bad request
// before any provider is asked; the provider's own failures map through the
// engine's table: a missing, invalid, expired or used proof is 403
// human-verification-required, an over-long proof the provider admits is 400,
// and a provider or store that cannot answer is 503. It reports whether the
// request may go on; a false return has already set the answer, and nothing has
// been sent.
func (r *routes) checkHumanProof(c *call, proof string) bool {
	if r.human == nil || !r.human.Required() {
		return true
	}
	if len(proof) > mount.HumanProofMaxBytes {
		c.fail(mount.CodeBadRequest)
		return false
	}
	if err := r.human.Verify(c.ctx, authn.HumanProof{Token: proof, RemoteIP: c.clientIP}); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}
