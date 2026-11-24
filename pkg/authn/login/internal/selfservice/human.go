package selfservice

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func (r *routes) checkHumanProof(c *call, proof string) bool {
	if r.deps.Human == nil || !r.deps.Human.Required(authn.HumanScopeOther) {
		return true
	}
	if len(proof) > mount.HumanProofMaxBytes {
		r.fail(c, fmt.Errorf("%w: the human-verification proof is over its limit", authn.ErrHumanProofTooLong))
		return false
	}
	if err := r.deps.Human.Verify(c.ctx, authn.HumanProof{Token: proof, RemoteIP: c.clientIP}, authn.HumanScopeOther); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}
