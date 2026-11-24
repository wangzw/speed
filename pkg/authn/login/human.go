package login

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func isHumanProofRejection(err error) bool {
	return errors.Is(err, authn.ErrHumanVerificationRequired) || errors.Is(err, authn.ErrHumanProofTooLong)
}

// checkHumanProof checks the reset email branch's proof before any send budget is spent; the nil gate is unreachable defence for a product built without one.
func (r *routes) checkHumanProof(c *call, proof string) bool {
	if r.human == nil || !r.human.Required(authn.HumanScopeOther) {
		return true
	}
	if len(proof) > mount.HumanProofMaxBytes {
		r.fail(c, fmt.Errorf("%w: the human-verification proof is over its limit", authn.ErrHumanProofTooLong))
		return false
	}
	if err := r.human.Verify(c.ctx, authn.HumanProof{Token: proof, RemoteIP: c.clientIP}, authn.HumanScopeOther); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}
