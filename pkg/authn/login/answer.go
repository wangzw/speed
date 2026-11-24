package login

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const fieldUsername = "username"

const fieldDisplayName = "display_name"

const fieldPassword = "password"

const (
	fieldFactor = "factor"
	fieldCode   = "code"
)

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
		if rec.State != stateFirst && rec.State != stateSignup && rec.State != stateReprove {
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
	if !r.admitSends(c) {
		return c.result()
	}

	switch in.Step {
	case stepAnswer:
		if rec.State == stateSignup {
			r.applySignupAnswer(c, token, echo, rec, raw, in.Input)
		} else {
			r.applyFirstSubmit(c, token, echo, rec, raw, in.Input, in.HumanProof)
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

// challengeFactor is the only place the engine fills FlowState.Source.
func (r *routes) challengeFactor(c *call, token, echo string, rec record, raw []byte, factorID, proof string) {
	f := r.flowState(rec, token)
	f.Source = c.clientIP
	// The proof travels to the factor and its code engine, which checks and consumes it once; the engine never checks it itself.
	f.Proof = proof
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
		if cause := step.CauseOf(got); isHumanProofRejection(cause) {
			r.fail(c, cause)
			return
		}
		if errors.Is(step.CauseOf(got), authn.ErrPhoneRequired) {
			c.forbidden(mount.CodePhoneRequired)
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

func offeredContains(offered []factorView, id string) bool {
	for _, f := range offered {
		if f.ID == id {
			return true
		}
	}
	return false
}
