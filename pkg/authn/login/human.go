package login

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// isHumanProofRejection reports whether err is the human-verification gate
// refusing the proof a request presented, rather than a credential that did not
// hold: ErrHumanVerificationRequired for a missing, invalid, expired or already
// used proof, and ErrHumanProofTooLong for one over the field's bound. Each
// keeps its own 403 or 400 and neither is a failed login.
func isHumanProofRejection(err error) bool {
	return errors.Is(err, authn.ErrHumanVerificationRequired) || errors.Is(err, authn.ErrHumanProofTooLong)
}

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
		r.fail(c, fmt.Errorf("%w: the human-verification proof is over its limit", authn.ErrHumanProofTooLong))
		return false
	}
	if err := r.human.Verify(c.ctx, authn.HumanProof{Token: proof, RemoteIP: c.clientIP}); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}
