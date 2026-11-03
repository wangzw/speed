package login

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// fieldUsername is the input key the pending-signup step carries. The engine
// handles it itself: no method is asked for the username a first login opens an
// account with.
const fieldUsername = "username"

// fieldDisplayName is the input key a phone first login may carry beside the
// username. It is optional, and no other first login reads it: a social or
// enterprise account takes its display name from the provider.
const fieldDisplayName = "display_name"

// fieldPassword is the input key a social first login carries beside the
// username. It is the password the account is opened with, and no other first
// login reads it.
const fieldPassword = "password"

// fieldFactor and fieldCode are the input keys a second-factor step carries.
const (
	fieldFactor = "factor"
	fieldCode   = "code"
)

// answer handles one step of a flow the request is bound to. It checks the
// binding, then the step number and the step itself; the body was bound by the
// entry point before the handler.
func (r *routes) answer(ctx context.Context, in answerInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	c.union = true
	token, echo, rec, raw, ok := r.requireBinding(c, true)
	if !ok {
		return c.result()
	}
	if in.Seq != rec.Seq {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	switch in.Step {
	case stepAnswer:
		// An answer submitted at the first factor goes to the method; one
		// submitted at the pending signup carries the username the engine opens
		// the account with, and no method is asked.
		if rec.State != stateFirst && rec.State != stateSignup {
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
	case stepFactor:
		if rec.State != stateSecond {
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
	case stepEnroll:
		if rec.State != stateEnroll {
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
	case stepComplete:
		if rec.State != stateReady {
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
	default:
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if in.Step != stepComplete && rec.Answers >= maxAnswers {
		failed := rec
		failed.State = stateFailed
		if !r.swap(c, token, raw, failed) {
			return c.result()
		}
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if err := checkFields(in.Input, maxAnswerFieldBytes); err != nil {
		r.fail(c, err)
		return c.result()
	}
	// A submission that carries the phone a first-factor method declares may
	// send a code, so the gate is checked before the step switch. A second
	// factor's own send is checked where its challenge starts.
	if r.methodSubmissionCarriesPhone(rec.Method, in.Input) {
		if !r.checkHumanProof(c, in.HumanProof) {
			return c.result()
		}
	}
	// The source-address allowance is checked before any step can reach a
	// method, so an answer from a source already at its limit sends nothing.
	if !r.admitSends(c) {
		return c.result()
	}

	switch in.Step {
	case stepAnswer:
		if rec.State == stateSignup {
			r.applySignupAnswer(c, token, echo, rec, raw, in.Input)
		} else {
			r.applyFirstSubmit(c, token, echo, rec, raw, in.Input)
		}
	case stepFactor:
		r.applyFactor(c, token, echo, rec, raw, in.Input, in.HumanProof)
	case stepEnroll:
		r.applyEnroll(c, token, echo, rec, raw, in.Input["kind"], in.Input)
	case stepComplete:
		r.complete(c, token, rec, raw)
	}
	return c.result()
}

// applyFactor handles a second-factor step: the first step for a factor starts
// its challenge, and a step whose factor is the one already challenged verifies
// it.
func (r *routes) applyFactor(c *call, token, echo string, rec record, raw []byte, fields map[string]string, proof string) {
	factorID := fields[fieldFactor]
	if factorID == "" {
		c.fail(mount.CodeBadRequest)
		return
	}
	if !offeredContains(rec.MFA.Offered, factorID) {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	if rec.MFA.ChallengeRef != factorID {
		r.challengeFactor(c, token, echo, rec, raw, factorID, proof)
		return
	}
	f := r.flowState(rec, token)
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: c.clientInfo()}
	got, ok := r.invoke(c.ctx, "factor", func(cctx context.Context) (authn.Step, error) {
		return r.mfa.Verify(cctx, f, factorID, in)
	})
	if !ok {
		c.serverError()
		return
	}
	if !r.recordSends(c, token, raw, &rec, got) {
		return
	}
	switch step.KindOf(got) {
	case step.KindPassed:
		rec.MFA.Passed = passOfPassed(step.PassOf(got))
		rec.State = stateReady
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		writeComplete(c, rec)
	case step.KindRefused:
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, outcomeFailed)
		c.unauthorized(mount.CodeLoginFailed)
	case step.KindThrottled:
		r.reportLogin(c.ctx, rec, outcomeThrottled)
		c.throttled(step.RetryAfterOf(got))
	case step.KindUnavailable:
		r.reportLogin(c.ctx, rec, outcomeUnavailable)
		c.unavailable()
	default:
		r.logger.Error(ModuleName + ": a factor gave an answer a verification step may not give")
		c.serverError()
	}
}

// challengeFactor starts a verification on a chosen factor and keeps its state
// for the step that answers it. It is the only place the engine fills
// FlowState.Source: the factor has no request of its own, so the address comes
// from this request and is the send's source when the factor sends a code.
func (r *routes) challengeFactor(c *call, token, echo string, rec record, raw []byte, factorID, proof string) {
	// Choosing an SMS factor sends a code to the account's proven phone, so the
	// gate is checked before the factor is asked and before any send budget is
	// spent. The kind comes from the offered factor, never from its name.
	if factorKindOf(rec.MFA.Offered, factorID) == string(authn.FactorSMS) {
		if !r.checkHumanProof(c, proof) {
			return
		}
	}
	f := r.flowState(rec, token)
	f.Source = c.clientIP
	got, ok := r.invoke(c.ctx, "factor", func(cctx context.Context) (authn.Step, error) {
		return r.mfa.Challenge(cctx, f, factorID)
	})
	if !ok {
		c.serverError()
		return
	}
	if !r.recordSends(c, token, raw, &rec, got) {
		return
	}
	switch step.KindOf(got) {
	case step.KindPrompt:
		rec.MFA.ChallengeRef = factorID
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, promptView(step.FieldsOf(got)), echo)
	case step.KindData:
		rec.MFA.ChallengeRef = factorID
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, stepView{Kind: nextWebAuthn, Options: step.DataOf(got)}, echo)
	case step.KindRefused:
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, outcomeFailed)
		c.unauthorized(mount.CodeLoginFailed)
	case step.KindThrottled:
		r.reportLogin(c.ctx, rec, outcomeThrottled)
		c.throttled(step.RetryAfterOf(got))
	case step.KindUnavailable:
		r.reportLogin(c.ctx, rec, outcomeUnavailable)
		c.unavailable()
	default:
		r.logger.Error(ModuleName + ": a factor gave an answer a challenge step may not give")
		c.serverError()
	}
}

// offeredContains reports whether a factor identifier is one the flow offered.
func offeredContains(offered []factorView, id string) bool {
	for _, f := range offered {
		if f.ID == id {
			return true
		}
	}
	return false
}
