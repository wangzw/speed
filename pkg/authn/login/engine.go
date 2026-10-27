package login

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
)

// errNoUsableFactor is a login whose account has factors but none the engine can
// offer, which reads as a failed credential and never as an account with no
// factor.
var errNoUsableFactor = fmt.Errorf("%w: no usable factor", authn.ErrInvalidCredentials)

// invoke runs one call to a login method or a factor under the call budget,
// recovering a panic and discarding an answer that arrives after the budget.
// ok is false only when the call gave no answer at all, which the caller writes
// as a server error; every other defect is answered as the method's outage.
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

// countAnswer folds one accepted client answer into the record's count, which
// the per-flow limit is read against. A call that is the flow's first move and
// not a client's answer counts none.
func countAnswer(rec *record, answered bool) {
	if answered {
		rec.Answers++
	}
}

// sendsLeft is how many SMS sends a flow may still make.
func (r *routes) sendsLeft(rec record) int {
	left := r.settings.perFlowSends() - rec.Sends
	if left < 0 {
		return 0
	}
	return left
}

// flowState is the view a method or factor is handed of the flow.
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

// flowAccount is the account a flow's steps act for. For a link and an
// enrollment it is the effective account, which is where those terminal actions
// land. A step-up proves the factors of the account that started it, so when an
// impersonating identity started the flow its steps act for the operator.
func flowAccount(rec record) authn.AccountID {
	if rec.Purpose == authn.PurposeStepUp && rec.Actor != "" {
		return authn.AccountID(rec.Actor)
	}
	return authn.AccountID(rec.Account)
}

// swap writes the record built from old, refusing the request as an invalid flow
// when another writer moved the record first.
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

// recordSends folds a method's or factor's send report into the record. It
// returns false and ends the flow when the report overstates what the call was
// allowed to send, which would widen the per-flow limit: the record is swapped
// to its tombstone and the request is answered as the method's outage. A report
// is also what spends the source address's allowance: the pre-throttle counts
// verification messages rather than flow starts, and a call that sent nothing
// spends nothing. admitSends is the read-only check at request entry that keeps
// a source already at its limit from reaching the method and sending at all.
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

// applyFirstSubmit advances a flow whose first-factor answer is a client
// submission. It is the answer route's work for a flow in the first state.
func (r *routes) applyFirstSubmit(c *call, token, echo string, rec record, raw []byte, fields map[string]string) {
	m, ok := r.method(rec.Method)
	if !ok {
		r.fail(c, fmt.Errorf("%w: the flow names a method not taken in", authn.ErrFlowInvalid))
		return
	}
	f := r.flowState(rec, token)
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

// applyFirstBegin runs the first call of a flow started at the first factor. It
// is not a client answer, so it moves the step number but counts no answer.
func (r *routes) applyFirstBegin(c *call, token, echo string, rec record, raw []byte, fields map[string]string) {
	m, ok := r.method(rec.Method)
	if !ok {
		r.fail(c, fmt.Errorf("%w: the flow names a method not taken in", authn.ErrFlowInvalid))
		return
	}
	// The provider the flow's callback path must name is fixed with this first
	// call and written in the swap below: the start input's provider for an
	// enterprise one, the method's own name for a social one.
	rec.Provider = flowProvider(rec.Method, fields)
	s := authn.Start{
		Purpose:   rec.Purpose,
		Account:   flowAccount(rec),
		Fields:    fields,
		Client:    c.clientInfo(),
		SendsLeft: r.sendsLeft(rec),
		Locale:    rec.Locale,
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

// migrateFirst turns a first-factor method's answer into the next record state,
// swaps it and writes the reply. callback marks the callback route, whose reply
// for a completed cookie login is a 303 rather than a body; answered marks a
// client submission, which is what counts against the per-flow answer limit.
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
		rec.Seq++
		countAnswer(&rec, answered)
		rec.MethodStep = ""
		rec.MethodState = nil
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, outcomeFailed)
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
		// A first factor answers a proof, a prompt, a redirect, a refusal or an
		// outage; anything else is its defect.
		r.logger.Error(ModuleName+": a login method gave an answer a first factor may not give",
			slog.String("method", rec.Method), slog.String("answer", step.KindOf(got).String()))
		c.serverError()
	}
}

// resolveAndDecide resolves the account a proof names and decides what the flow
// does next: a second factor, a registration, or ready. The record it writes
// carries the account, the proof and, for a login, the policy outcome.
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
	// Only a login resolves to the account it lands on. A link, a step-up and an
	// enrollment are bound to the account the flow was started on, so their record
	// keeps it even when the proof was the operator's own.
	if rec.Purpose == authn.PurposeLogin {
		rec.Account = string(res.Account)
	}
	p := proof
	rec.Proof = &p
	// The first factor's private step state is spent once it proves. Carrying
	// it into the next state would hand it to a registration or a second
	// factor as that step's own in-progress state, which is what a redirect
	// first factor leaves behind.
	rec.MethodStep = ""
	rec.MethodState = nil
	if res.Outcome == resolve.OutcomeMerged {
		rec.Merged = true
	}
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
		// A callback is a top-level navigation, so a flow that still needs a
		// second or a registration factor sends the user agent back to the
		// return target with the flow left in the cookie; the front end resumes
		// with GET flows/current.
		if callback {
			c.redirect(http.StatusSeeOther, r.returnTarget(rec))
			return
		}
		r.replyStep(c, rec, r.nextOf(rec), echo)
	default:
		c.serverError()
	}
}

// decidePolicy sets the flow's next state from the multi-factor policy. A login
// with a second factor becomes second, a login the policy requires a factor of
// becomes enroll, and every other purpose and a login with no factor requirement
// becomes ready.
func (r *routes) decidePolicy(ctx context.Context, rec *record, proof authn.Proof) error {
	if rec.Purpose != authn.PurposeLogin {
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
	default:
		return fmt.Errorf("%w: the policy gave an outcome outside its set", authn.ErrUnavailable)
	}
	return nil
}

// factorViews renders the offered factors for the client.
func factorViews(records []authn.FactorRecord) []factorView {
	out := make([]factorView, len(records))
	for i, f := range records {
		out[i] = factorView{ID: f.ID, Kind: string(f.Kind), Label: f.Label}
	}
	return out
}

// replyStep writes a reply that keeps a flow open, echoing the flow token only
// when the request carried it in the header.
func (r *routes) replyStep(c *call, rec record, next stepView, echo string) {
	c.wire(flowReply{Seq: rec.Seq, State: rec.State, FlowToken: echo, Next: next})
}

// replyReady writes the reply of a flow that reached ready: for the callback
// route of a cookie login the session is made here and the user agent is sent to
// the return target; for a callback of another purpose the user agent is landed
// the same way, with the flow left in the cookie for the client to complete; a
// token login's callback that is a navigation is landed the same way too, while
// one that is an API call is answered the completion body; for a submit answer
// the client takes the result with step=complete.
func (r *routes) replyReady(c *call, token string, rec record, callback bool, echo string) {
	if callback {
		switch {
		case rec.Purpose == authn.PurposeLogin && rec.Delivery == deliverySession:
			r.completeCallbackLogin(c, token, rec)
			return
		case rec.Purpose == authn.PurposeLogin && rec.Delivery == deliveryToken:
			// A token cannot ride a redirect URL: an API callback waits for the
			// client to take the completion. A callback that is a top-level
			// navigation cannot read that body, so it is landed like every other
			// navigation landing: no session is made, the flow stays at ready in
			// the cookie, and the client takes the token with a later complete.
			if c.navigation() {
				c.redirect(http.StatusSeeOther, r.returnTarget(rec))
				return
			}
			r.replyStep(c, rec, stepView{Kind: nextComplete}, echo)
			return
		case rec.Purpose != authn.PurposeLogin:
			// A callback of a purpose other than login is a top-level navigation
			// too, so it lands the user agent the same way. No session is made:
			// the flow stays at ready in the cookie, the host's callback page
			// reads it with GET flows/current, and the terminal action runs when
			// the client answers complete.
			c.redirect(http.StatusSeeOther, r.returnTarget(rec))
			return
		}
	}
	writeComplete(c, rec)
}

// returnTarget is where a callback lands the user agent: the return target the
// flow recorded, or the deployment's default when the flow recorded none.
func (r *routes) returnTarget(rec record) string {
	if rec.ReturnTo != "" {
		return rec.ReturnTo
	}
	return r.defaultReturnTarget()
}

// completeCallbackLogin runs the terminal action of a callback that finished a
// cookie login. The record was just left at ready by the resolution, so the
// ready->done tombstone is swapped here first, exactly as complete does on the
// answer route: the callback's terminal action runs at most once and an Open
// POST flows/answer {"step":"complete"} against the same flow is refused.
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
