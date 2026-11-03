package selfservice

import (
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// checkHumanProof holds a send to the deployment's human-verification gate. It
// is the email branch's check: the phone routes hand their proof to the code
// engine, which checks and consumes it once, while the email code goes out over
// the email channel and has no engine to check it.
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
	if r.deps.Human == nil || !r.deps.Human.Required() {
		return true
	}
	if len(proof) > mount.HumanProofMaxBytes {
		c.fail(mount.CodeBadRequest)
		return false
	}
	if err := r.deps.Human.Verify(c.ctx, authn.HumanProof{Token: proof, RemoteIP: c.clientIP}); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}
