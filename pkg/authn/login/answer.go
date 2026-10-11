package login

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// answerRequest is the body of POST flows/answer.
type answerRequest struct {
	Seq   int               `json:"seq"`
	Step  string            `json:"step"`
	Input map[string]string `json:"input"`
}

// fieldFactor and fieldCode are the input keys a second-factor step carries.
const (
	fieldFactor = "factor"
	fieldCode   = "code"
)

// answer handles one step of a flow the request is bound to. It checks the
// binding before the body, then the content type while reading it, then the step
// number and the step itself.
func (r *routes) answer(c *gin.Context) {
	if !contentTypeJSON(c) {
		return
	}
	token, echo, rec, raw, ok := r.requireBinding(c, true)
	if !ok {
		return
	}
	var req answerRequest
	if !mount.Decode(c, &req) {
		return
	}
	if req.Seq != rec.Seq {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	switch req.Step {
	case stepAnswer:
		if rec.State != stateFirst {
			mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
			return
		}
	case stepFactor:
		if rec.State != stateSecond {
			mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
			return
		}
	case stepEnroll:
		if rec.State != stateEnroll {
			mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
			return
		}
	case stepComplete:
		if rec.State != stateReady {
			mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
			return
		}
	default:
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if req.Step != stepComplete && rec.Answers >= maxAnswers {
		failed := rec
		failed.State = stateFailed
		if !r.swap(c, token, raw, failed) {
			return
		}
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if err := checkFields(req.Input); err != nil {
		r.fail(c, err)
		return
	}

	switch req.Step {
	case stepAnswer:
		r.applyFirstSubmit(c, token, echo, rec, raw, req.Input)
	case stepFactor:
		r.applyFactor(c, token, echo, rec, raw, req.Input)
	case stepEnroll:
		r.applyEnroll(c, token, echo, rec, raw, req.Input["kind"], req.Input)
	case stepComplete:
		r.complete(c, token, rec, raw)
	}
}

// applyFactor handles a second-factor step: the first step for a factor starts
// its challenge, and a step whose factor is the one already challenged verifies
// it.
func (r *routes) applyFactor(c *gin.Context, token, echo string, rec record, raw []byte, fields map[string]string) {
	ctx := c.Request.Context()
	factorID := fields[fieldFactor]
	if factorID == "" {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if !offeredContains(rec.MFA.Offered, factorID) {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if rec.MFA.ChallengeRef != factorID {
		r.challengeFactor(c, token, echo, rec, raw, factorID)
		return
	}
	f := r.flowState(rec, token)
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: requestClient(c)}
	got, ok := r.invoke(ctx, "factor", func(cctx context.Context) (authn.Step, error) {
		return r.mfa.Verify(cctx, f, factorID, in)
	})
	if !ok {
		serverError(c)
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
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(ctx, rec, outcomeFailed)
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	case step.KindThrottled:
		r.reportLogin(ctx, rec, outcomeThrottled)
		mount.Throttled(c, step.RetryAfterOf(got))
	case step.KindUnavailable:
		r.reportLogin(ctx, rec, outcomeUnavailable)
		mount.Unavailable(c)
	default:
		r.logger.Error(ModuleName + ": a factor gave an answer a verification step may not give")
		serverError(c)
	}
}

// challengeFactor starts a verification on a chosen factor and keeps its state
// for the step that answers it.
func (r *routes) challengeFactor(c *gin.Context, token, echo string, rec record, raw []byte, factorID string) {
	ctx := c.Request.Context()
	f := r.flowState(rec, token)
	got, ok := r.invoke(ctx, "factor", func(cctx context.Context) (authn.Step, error) {
		return r.mfa.Challenge(cctx, f, factorID)
	})
	if !ok {
		serverError(c)
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
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(ctx, rec, outcomeFailed)
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	case step.KindThrottled:
		r.reportLogin(ctx, rec, outcomeThrottled)
		mount.Throttled(c, step.RetryAfterOf(got))
	case step.KindUnavailable:
		r.reportLogin(ctx, rec, outcomeUnavailable)
		mount.Unavailable(c)
	default:
		r.logger.Error(ModuleName + ": a factor gave an answer a challenge step may not give")
		serverError(c)
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
