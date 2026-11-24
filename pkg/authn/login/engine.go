package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
)

var errNoUsableFactor = fmt.Errorf("%w: no usable factor", authn.ErrInvalidCredentials)

func (p *product) invoke(ctx context.Context, method string, call func(ctx context.Context) (authn.Step, error)) (authn.Step, bool) {
	cctx, cancel := context.WithTimeout(ctx, p.settings.callBudget())
	defer cancel()

	var got authn.Step
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				p.logger.Error(ModuleName+": a login method panicked",
					slog.String("method", method), slog.String("cause", fmt.Sprint(r)),
					slog.String("stack", string(debug.Stack())))
			}
		}()
		var err error
		got, err = call(cctx)
		if err != nil {
			panicked = true
			p.logger.Warn(ModuleName+": a login method failed",
				slog.String("method", method), slog.String("cause", err.Error()))
		}
	}()
	if panicked || cctx.Err() != nil {
		return authn.StepUnavailable(fmt.Errorf("%w: the method %s did not answer", authn.ErrUnavailable, method)), true
	}
	if step.KindOf(got) == step.KindUnanswered {
		p.logger.Error(ModuleName+": a login method gave no answer its call may take",
			slog.String("method", method))
		return authn.Step{}, false
	}
	if n := len(step.PrivateOf(got)); n > maxPrivateBytes {
		p.logger.Error(ModuleName+": a login method gave more private state than the surface allows",
			slog.String("method", method), slog.Int("bytes", n))
		return authn.StepUnavailable(fmt.Errorf("%w: the method %s gave oversized private state",
			authn.ErrUnavailable, method)), true
	}
	return got, true
}

func countAnswer(rec *record, answered bool) {
	if answered {
		rec.Answers++
	}
}

func (r *routes) sendsLeft(rec record) int {
	left := r.settings.perFlowSends() - rec.Sends
	if left < 0 {
		return 0
	}
	return left
}

func (r *routes) flowState(rec record, token string) authn.FlowState {
	return authn.FlowState{
		ID:        tokenDigest(token)[:16],
		Purpose:   rec.Purpose,
		Step:      rec.MethodStep,
		Provider:  rec.Provider,
		Account:   flowAccount(rec),
		Session:   authn.SessionID(rec.Session),
		Private:   rec.MethodState,
		SendsLeft: r.sendsLeft(rec),
		Locale:    rec.Locale,
	}
}

// flowAccount is the account a flow's steps act for: while an impersonating identity runs a step-up, its steps prove the operator's factors.
func flowAccount(rec record) authn.AccountID {
	if rec.Purpose == authn.PurposeStepUp && rec.Actor != "" {
		return authn.AccountID(rec.Actor)
	}
	return authn.AccountID(rec.Account)
}

func (r *routes) swap(c *call, token string, old []byte, rec record) bool {
	ok, err := r.advance(c.ctx, token, old, rec)
	if err != nil {
		r.fail(c, err)
		return false
	}
	if !ok {
		c.unauthorized(mount.CodeFlowInvalid)
		return false
	}
	return true
}

// recordSends ends the flow as the method's outage when a report exceeds SendsLeft: accepting it would widen the per-flow limit.
func (r *routes) recordSends(c *call, token string, raw []byte, rec *record, got authn.Step) bool {
	n := step.SendsOf(got)
	if n < 0 {
		n = 0
	}
	if n > r.sendsLeft(*rec) {
		r.logger.Error(ModuleName+": a login method reported more sends than it was allowed",
			slog.Int("reported", n), slog.Int("allowed", r.sendsLeft(*rec)))
		failed := *rec
		failed.State = stateFailed
		_, _ = r.advance(c.ctx, token, raw, failed)
		c.unavailable()
		return false
	}
	if n > 0 && !r.chargeSends(c, n) {
		return false
	}
	rec.Sends += n
	return true
}

func (r *routes) applyFirstSubmit(c *call, token, echo string, rec record, raw []byte, fields map[string]string, proof string) {
	m, ok := r.method(rec.Method)
	if !ok {
		r.fail(c, fmt.Errorf("%w: the flow names a method not taken in", authn.ErrFlowInvalid))
		return
	}
	f := r.flowState(rec, token)
	f.Proof = proof
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: c.clientInfo()}
	got, ok2 := r.invoke(c.ctx, rec.Method, func(cctx context.Context) (authn.Step, error) {
		return m.Answer(cctx, f, in)
	})
	if !ok2 {
		c.serverError()
		return
	}
	r.migrateFirst(c, token, echo, rec, raw, got, false, true)
}

func (r *routes) applyFirstBegin(c *call, token, echo string, rec record, raw []byte, fields map[string]string, proof string) {
	m, ok := r.method(rec.Method)
	if !ok {
		r.fail(c, fmt.Errorf("%w: the flow names a method not taken in", authn.ErrFlowInvalid))
		return
	}
	// The provider is fixed with this first call; a callback path is matched against it.
	rec.Provider = flowProvider(rec.Method, fields)
	s := authn.Start{
		Purpose:   rec.Purpose,
		Account:   flowAccount(rec),
		Fields:    fields,
		Client:    c.clientInfo(),
		SendsLeft: r.sendsLeft(rec),
		Locale:    rec.Locale,
		Proof:     proof,
	}
	got, ok2 := r.invoke(c.ctx, rec.Method, func(cctx context.Context) (authn.Step, error) {
		return m.Begin(cctx, s)
	})
	if !ok2 {
		c.serverError()
		return
	}
	r.migrateFirst(c, token, echo, rec, raw, got, false, false)
}

func (r *routes) migrateFirst(c *call, token, echo string, rec record, raw []byte, got authn.Step, callback, answered bool) {
	if !r.recordSends(c, token, raw, &rec, got) {
		return
	}
	switch step.KindOf(got) {
	case step.KindPrompt:
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		countAnswer(&rec, answered)
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, promptView(step.FieldsOf(got)), echo)
	case step.KindRedirect:
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		countAnswer(&rec, answered)
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, stepView{Kind: nextRedirect, URL: step.RedirectOf(got), Method: "GET"}, echo)
	case step.KindData:
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		countAnswer(&rec, answered)
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, stepView{Kind: nextWebAuthn, Options: step.DataOf(got)}, echo)
	case step.KindRefused:
		countAnswer(&rec, answered)
		if !r.swap(c, token, raw, rec) {
			return
		}
		if cause := step.CauseOf(got); !isHumanProofRejection(cause) {
			r.reportLogin(c.ctx, rec, outcomeFailed)
		}
		r.refusedStep(c, step.CauseOf(got))
	case step.KindThrottled:
		r.reportLogin(c.ctx, rec, outcomeThrottled)
		c.throttled(step.RetryAfterOf(got))
	case step.KindUnavailable:
		r.reportLogin(c.ctx, rec, outcomeUnavailable)
		c.unavailable()
	case step.KindProven:
		r.resolveAndDecide(c, token, echo, rec, raw, step.ProofOf(got), callback)
	default:
		r.logger.Error(ModuleName+": a login method gave an answer a first factor may not give",
			slog.String("method", rec.Method), slog.String("answer", step.KindOf(got).String()))
		c.serverError()
	}
}

func (r *routes) resolveAndDecide(c *call, token, echo string, rec record, raw []byte, proof authn.Proof, callback bool) {
	res, err := r.resolver.Resolve(c.ctx, resolve.Request{
		Proof:   proof,
		Purpose: rec.Purpose,
		Account: flowAccount(rec),
	})
	if err != nil {
		rec.State = stateFailed
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, loginOutcomeOf(err))
		r.fail(c, err)
		return
	}
	if res.Pending != nil {
		r.beginSignup(c, token, echo, rec, raw, proof, res.Pending, callback)
		return
	}
	// Only a login takes its resolved account; the other purposes stay bound to the account the flow started on.
	if rec.Purpose == authn.PurposeLogin {
		if rec.State == stateReprove && res.Account != authn.AccountID(rec.Account) {
			rec.State = stateFailed
			if !r.swap(c, token, raw, rec) {
				return
			}
			r.reportLogin(c.ctx, rec, outcomeFailed)
			c.unauthorized(mount.CodeLoginFailed)
			return
		}
		rec.Account = string(res.Account)
	}
	p := proof
	rec.Proof = &p
	// The first factor's private step state is spent once it proves; carrying it on would hand it to another step.
	rec.MethodStep = ""
	rec.MethodState = nil
	if err := r.decidePolicy(c.ctx, &rec, proof); err != nil {
		rec.State = stateFailed
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, loginOutcomeOf(err))
		r.fail(c, err)
		return
	}
	if !r.swap(c, token, raw, rec) {
		return
	}
	switch rec.State {
	case stateReady:
		r.replyReady(c, token, rec, callback, echo)
	case stateSecond, stateEnroll:
		if callback {
			c.redirect(http.StatusSeeOther, r.returnTarget(rec))
			return
		}
		r.replyStep(c, rec, r.nextOf(rec), echo)
	default:
		c.serverError()
	}
}

func (r *routes) beginSignup(c *call, token, echo string, rec record, raw []byte, proof authn.Proof,
	pending *resolve.Pending, callback bool,
) {
	p := proof
	rec.Proof = &p
	rec.Pending = pending
	rec.State = stateSignup
	rec.MethodStep = ""
	rec.MethodState = nil
	if !r.swap(c, token, raw, rec) {
		return
	}
	if callback {
		c.redirect(http.StatusSeeOther, r.returnTarget(rec))
		return
	}
	r.replyStep(c, rec, r.nextOf(rec), echo)
}

func (r *routes) applySignupAnswer(c *call, token, echo string, rec record, raw []byte, fields map[string]string) {
	if rec.Pending == nil || rec.Proof == nil {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	var password, displayName string
	switch rec.Pending.Source {
	case resolve.SourceSocial:
		password = fields[fieldPassword]
	case resolve.SourcePhone:
		password = fields[fieldPassword]
		displayName = fields[fieldDisplayName]
	}
	res, err := r.resolver.Create(c.ctx, *rec.Pending, fields[fieldUsername], password, displayName)
	switch {
	case errors.Is(err, authn.ErrUsernameReserved):
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		c.fail(mount.CodeUsernameReserved)
		return
	case authn.IsUsernameShape(err):
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		c.fail(mount.CodeUsernameInvalid)
		return
	case errors.Is(err, authn.ErrInvalidIdentifier):
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		c.fail(mount.CodeBadRequest)
		return
	case errors.Is(err, authn.ErrPasswordPolicy):
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		c.fail(mount.CodeBadRequest)
		return
	case errors.Is(err, authn.ErrAccountExists):
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		c.fail(mount.CodeUsernameTaken)
		return
	case errors.Is(err, authn.ErrContactTaken):
		rec.State = stateFailed
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, loginOutcomeOf(err))
		c.failureEmail = maskEmail(rec.Pending.Email)
		r.fail(c, err)
		return
	case err != nil:
		rec.State = stateFailed
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, loginOutcomeOf(err))
		r.fail(c, err)
		return
	}
	rec.Account = string(res.Account)
	rec.Pending = nil
	rec.Seq++
	rec.Answers++
	if err := r.decidePolicy(c.ctx, &rec, *rec.Proof); err != nil {
		rec.State = stateFailed
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, loginOutcomeOf(err))
		r.fail(c, err)
		return
	}
	if !r.swap(c, token, raw, rec) {
		return
	}
	if rec.State == stateReady {
		writeComplete(c, rec)
		return
	}
	r.replyStep(c, rec, r.nextOf(rec), echo)
}

func (r *routes) decidePolicy(ctx context.Context, rec *record, proof authn.Proof) error {
	if rec.Purpose != authn.PurposeLogin {
		if rec.Purpose == authn.PurposeStepUp && len(rec.MFA.Offered) > 0 {
			rec.State = stateSecond
			rec.MFA.Required = true
			return nil
		}
		rec.State = stateReady
		return nil
	}
	outcome, offered, err := r.mfa.Require(ctx, authn.AccountID(rec.Account), authn.AccountID(rec.Account), proof)
	if err != nil {
		return fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
	}
	switch outcome {
	case authn.MFANone:
		rec.State = stateReady
	case authn.MFAFactor:
		if len(offered) == 0 {
			return errNoUsableFactor
		}
		rec.State = stateSecond
		rec.MFA.Required = true
		rec.MFA.Offered = factorViews(offered)
	case authn.MFAEnroll:
		rec.State = stateEnroll
		multi, terr := r.tenantRequiresMFA(ctx, authn.AccountID(rec.Account))
		if terr != nil {
			return fmt.Errorf("%w: %w", authn.ErrUnavailable, terr)
		}
		rec.MFA.EnrollMulti = multi
	default:
		return fmt.Errorf("%w: the policy gave an outcome outside its set", authn.ErrUnavailable)
	}
	return nil
}

// tenantRequiresMFA reports whether a tenant the account is an active member of requires multi-factor authentication; a failed read is an error, never "no requirement".
func (r *routes) tenantRequiresMFA(ctx context.Context, account authn.AccountID) (bool, error) {
	if r.tenantMFA == nil {
		return false, nil
	}
	return r.tenantMFA.RequiresMFA(ctx, account)
}

func factorViews(records []authn.FactorRecord) []factorView {
	out := make([]factorView, len(records))
	for i, f := range records {
		out[i] = factorView{ID: f.ID, Kind: string(f.Kind), Label: f.Label}
	}
	return out
}

func (r *routes) replyStep(c *call, rec record, next stepView, echo string) {
	c.wire(flowReply{Seq: rec.Seq, State: rec.State, FlowToken: echo, Next: next})
}

func (r *routes) replyReady(c *call, token string, rec record, callback bool, echo string) {
	if callback {
		switch {
		case rec.Purpose == authn.PurposeLogin && rec.Delivery == deliverySession:
			r.completeCallbackLogin(c, token, rec)
			return
		case rec.Purpose == authn.PurposeLogin && rec.Delivery == deliveryToken:
			if c.navigation() {
				c.redirect(http.StatusSeeOther, r.returnTarget(rec))
				return
			}
			r.replyStep(c, rec, stepView{Kind: nextComplete}, echo)
			return
		case rec.Purpose != authn.PurposeLogin:
			c.redirect(http.StatusSeeOther, r.returnTarget(rec))
			return
		}
	}
	writeComplete(c, rec)
}

func (r *routes) returnTarget(rec record) string {
	if rec.ReturnTo != "" {
		return rec.ReturnTo
	}
	return r.defaultReturnTarget()
}

// completeCallbackLogin swaps ready to done before acting, so the callback's terminal action runs at most once.
func (r *routes) completeCallbackLogin(c *call, token string, rec record) {
	raw, err := json.Marshal(rec)
	if err != nil {
		c.serverError()
		return
	}
	done := rec
	done.State = stateDone
	if !r.swap(c, token, raw, done) {
		return
	}
	r.completeLogin(c, rec, true)
}
