package login

import (
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// checkHumanProof holds a send to the deployment's human-verification gate. A
// deployment with no gate, and one whose gate is off, require nothing and are
// skipped entirely: the request goes on as it did before. A proof over the
// field's bound is a bad request before any provider is asked. The provider's
// own failures map through the engine's table: a missing, invalid, expired or
// used proof is 403 human-verification-required, an over-long proof the
// provider admits is 400, and a provider or store that cannot answer is 503. It
// reports whether the request may go on; a false return has already set the
// answer, and nothing has been sent.
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

// submissionCarriesPhone reports whether a method's descriptor declares a phone
// input and this submission supplies it. A method that only asks for the number
// sends nothing, so a submission that leaves the field empty is not a send the
// gate covers. The kind comes from the descriptor, never from a method's name.
func submissionCarriesPhone(d authn.MethodDescriptor, fields map[string]string) bool {
	for _, f := range d.Fields {
		if f.Kind == authn.FieldPhone && fields[f.Name] != "" {
			return true
		}
	}
	return false
}

// methodSubmissionCarriesPhone is submissionCarriesPhone for a method the engine
// knows by name. A name the deployment does not take in carries no phone: the
// route refuses it before any method runs.
func (r *routes) methodSubmissionCarriesPhone(name string, fields map[string]string) bool {
	m, ok := r.method(name)
	if !ok {
		return false
	}
	return submissionCarriesPhone(m.Describe(), fields)
}

// factorKindOf is the kind of one of the factors a flow offers, or "" when the
// identifier is not among them. The kind is what decides whether choosing the
// factor sends a verification message.
func factorKindOf(offered []factorView, id string) string {
	for _, f := range offered {
		if f.ID == id {
			return f.Kind
		}
	}
	return ""
}
