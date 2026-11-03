package login

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The two carriers a start may ask the flow token to travel in.
const (
	bindingCookie = "cookie"
	bindingHeader = "header"
)

// startFlow begins a login flow. It reads no cookie, so a stale session cookie
// in the browser does not stand in the way of logging in.
func (r *routes) startFlow(ctx context.Context, in startInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.admitSends(c) {
		return c.result()
	}
	if _, ok := r.method(in.Method); !ok {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	disabled, err := r.settings.disabledMethods()
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	if slices.Contains(disabled, in.Method) {
		c.forbidden(mount.CodeMethodDisabled)
		return c.result()
	}
	delivery := in.Delivery
	if delivery == "" {
		delivery = deliverySession
	}
	if delivery != deliverySession && delivery != deliveryToken {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if !r.deliveryAllowed(delivery) || (delivery == deliveryToken && r.tokens == nil) {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if in.ReturnTo != "" {
		if verr := checkReturnTo(in.ReturnTo, r.trustedOrigins); verr != nil {
			r.fail(c, verr)
			return c.result()
		}
	}
	binding := in.Binding
	if binding == "" {
		binding = bindingCookie
	}
	if binding != bindingCookie && binding != bindingHeader {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if verr := checkFields(in.Input, maxStartFieldBytes); verr != nil {
		r.fail(c, verr)
		return c.result()
	}
	token, err := newFlowToken()
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	now := r.clock.Now()
	ttl := r.settings.flowTTL()
	rec := record{
		Version:   recordVersion,
		Purpose:   authn.PurposeLogin,
		Method:    in.Method,
		State:     stateFirst,
		Binding:   tokenDigest(token),
		Delivery:  delivery,
		ReturnTo:  in.ReturnTo,
		Locale:    localeOf(c),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	raw, err := r.storeFlow(c.ctx, token, rec, ttl)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	echo := ""
	if binding == bindingHeader {
		echo = token
	} else {
		r.setFlowCookie(c, token, int(ttl.Seconds()))
	}
	r.applyFirstBegin(c, token, echo, rec, raw, in.Input, in.HumanProof)
	return c.result()
}

// startAuthenticated begins a link, a step-up or an enrollment on the identity
// the gateway authenticated. An impersonating identity is not refused: the flow
// is bound to the effective account and runs on the operator's own session.
func (r *routes) startAuthenticated(ctx context.Context, in startAuthenticatedInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	id, ok := authn.FromContext(c.ctx)
	if !ok || id.Account() == "" {
		c.unauthorized("")
		return c.result()
	}
	switch in.Purpose {
	case authn.PurposeLink, authn.PurposeStepUp, authn.PurposeEnroll:
	default:
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	// A link binds an external identity, an identity surface the platform gates
	// on a proved email address. The gate is checked before any record is
	// written, so a refused start leaves nothing behind; a state that cannot be
	// read is a 503 and never a pass. Step-up and enroll are not gated.
	if in.Purpose == authn.PurposeLink {
		signup, err := r.verification.SignupStateOf(c.ctx, id.Account())
		if err != nil {
			r.fail(c, fmt.Errorf("%w: reading the signup state: %w", authn.ErrUnavailable, err))
			return c.result()
		}
		if signup.State == authn.SignupPending {
			c.forbidden(mount.CodeContactRequired)
			return c.result()
		}
	}
	if err := checkFields(in.Input, maxStartFieldBytes); err != nil {
		r.fail(c, err)
		return c.result()
	}
	if in.ReturnTo != "" {
		if err := checkReturnTo(in.ReturnTo, r.trustedOrigins); err != nil {
			r.fail(c, err)
			return c.result()
		}
	}
	status, err := r.sessions.StatusFresh(c.ctx, id.Session())
	if err != nil {
		if isSessionGone(err) {
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
		r.fail(c, err)
		return c.result()
	}
	if !status.Active || !sessionOwns(id, status) {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	requireMulti, ok := r.startRequirement(c, id, in.Purpose)
	if !ok {
		return c.result()
	}

	binding := in.Binding
	if binding == "" {
		binding = bindingCookie
	}
	if binding != bindingCookie && binding != bindingHeader {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	delivery := deliverySession
	if status.Kind == authn.SessionToken {
		delivery = deliveryToken
	}

	if !r.admitSends(c) {
		return c.result()
	}
	token, err := newFlowToken()
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	now := r.clock.Now()
	ttl := r.settings.flowTTL()
	rec := record{
		Version:      recordVersion,
		Purpose:      in.Purpose,
		State:        stateFirst,
		Binding:      tokenDigest(token),
		Account:      string(id.Account()),
		Session:      string(id.Session()),
		Delivery:     delivery,
		ReturnTo:     in.ReturnTo,
		RequireMulti: requireMulti,
		Locale:       localeOf(c),
		CreatedAt:    now,
		ExpiresAt:    now.Add(ttl),
	}
	if id.Impersonating() {
		scope, _ := id.Scope()
		rec.Actor = string(id.Actor())
		rec.Impersonation = status.ImpersonationID
		rec.Scope = scope
	}

	var offered []factorView
	switch in.Purpose {
	case authn.PurposeLink:
		if _, ok := r.method(in.Method); !ok {
			c.fail(mount.CodeBadRequest)
			return c.result()
		}
		rec.Method = in.Method
	case authn.PurposeEnroll:
		rec.State = stateEnroll
	case authn.PurposeStepUp:
		start, method, stepUpOffered, insufficient, serr := r.stepUpStart(c.ctx, status, id.Account(), in.Method, in.Input["exclude_factor"])
		if serr != nil {
			r.fail(c, serr)
			return c.result()
		}
		if insufficient {
			c.stepUp(id, authn.FreshLocalMulti(r.settings.recentWindow()))
			return c.result()
		}
		rec.State = start
		rec.Method = method
		rec.MFA.Required = start == stateSecond
		rec.MFA.Offered = stepUpOffered
		offered = stepUpOffered
	}

	raw, err := r.storeFlow(c.ctx, token, rec, ttl)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	echo := ""
	if binding == bindingHeader {
		echo = token
	} else {
		r.setFlowCookie(c, token, int(ttl.Seconds()))
	}

	switch rec.State {
	case stateFirst:
		r.applyFirstBegin(c, token, echo, rec, raw, in.Input, in.HumanProof)
	case stateEnroll:
		r.applyEnroll(c, token, echo, rec, raw, in.Input["kind"], in.Input)
	default: // second
		r.replyStep(c, rec, stepView{Kind: nextFactor, Factors: offered}, echo)
	}
	return c.result()
}

// stepUpStart decides where a step-up flow starts and which method a first
// step runs. It asks the policy with the session's methods as the proof and the
// effective account as the requirement: the effective account's factors decide
// whether a second factor is needed, while the factors that may be offered are
// the ones the session's own account (the operator, under an impersonation)
// holds. A factor registered before the session authenticated keeps the flow at
// the second factor. When every factor was registered during the session, the
// flow starts at a first factor and offers those factors as the second step:
// proving the first factor again is what lets them strengthen the session. That
// first factor is the method the request names when the deployment takes it in;
// a request that names none, or names one the deployment does not take in,
// starts at the password instead. An account with no password to prove again,
// and no usable method to start with, is refused as insufficient, the same
// answer as an account with factors none of which may strengthen this session.
// A request that names the password an account cannot prove is the bad request
// a method this deployment does not take in gets. A start may name one factor to
// exclude from the offered list; when that leaves no factor at all, the deleting
// caller's last factor, a password proved again completes the flow on its own.
// An account without a password still gets the insufficient answer.
func (r *routes) stepUpStart(ctx context.Context, status authn.SessionStatus, requirement authn.AccountID, requested, excluded string) (start, method string, offered []factorView, insufficient bool, err error) {
	firstProof := authn.Proof{Methods: status.Methods}
	outcome, factors, err := r.mfa.Require(ctx, status.Account, requirement, firstProof)
	if err != nil {
		return "", "", nil, false, err
	}
	if outcome == authn.MFAFactor {
		// The caller may exclude one factor -- the one it is about to delete.
		// Excluding it never widens what may strengthen the session: it only
		// removes an entry from the offered list.
		candidates := factors
		if excluded != "" {
			candidates = make([]authn.FactorRecord, 0, len(factors))
			for _, f := range factors {
				if f.ID != excluded {
					candidates = append(candidates, f)
				}
			}
		}
		usable := make([]authn.FactorRecord, 0, len(candidates))
		for _, f := range candidates {
			// A factor activated at or after the session's authentication time
			// was registered during that login and may not strengthen it until
			// the account proves its password again.
			if f.ActivatedAt.Before(status.AuthTime) {
				usable = append(usable, f)
			}
		}
		if len(usable) > 0 {
			return stateSecond, "", factorViews(usable), false, nil
		}
		// No factor may strengthen the session yet. A non-empty list with no
		// factor activated before the session authenticated is the case a
		// password proved again unlocks.
		if len(factors) == 0 {
			return "", "", nil, true, nil
		}
		// Every factor the account held was excluded: the caller is deleting
		// the last one it could prove. A password proved again is the only
		// door left, so the deletion can still complete; it is also the only
		// way out when the reader lost the factor's device. An account with no
		// password is sent to re-login instead.
		if len(candidates) == 0 {
			hasPassword, perr := r.accountHasPassword(ctx, status.Account)
			if perr != nil {
				return "", "", nil, false, perr
			}
			if !hasPassword {
				return "", "", nil, true, nil
			}
			if _, ok := r.method(methodPassword); !ok {
				return "", "", nil, true, nil
			}
			return stateFirst, methodPassword, nil, false, nil
		}
		// No factor may strengthen the session yet. A first factor the request
		// names, and the deployment takes in, is what the person proves again;
		// proving it lets the factors registered during this session strengthen
		// the session. Only a request that names none, or names one this
		// deployment does not take in, falls back to the password.
		if requested != "" {
			if _, ok := r.method(requested); ok {
				if cerr := r.checkStartMethod(ctx, status.Account, requested); cerr != nil {
					return "", "", nil, false, cerr
				}
				return stateFirst, requested, factorViews(candidates), false, nil
			}
		}
		hasPassword, perr := r.accountHasPassword(ctx, status.Account)
		if perr != nil {
			return "", "", nil, false, perr
		}
		if !hasPassword {
			return "", "", nil, true, nil
		}
		if _, ok := r.method(methodPassword); !ok {
			return "", "", nil, true, nil
		}
		return stateFirst, methodPassword, factorViews(candidates), false, nil
	}
	if _, ok := r.method(requested); !ok {
		return "", "", nil, false, fmt.Errorf("%w: the step-up names a method not taken in", authn.ErrInvalidArgument)
	}
	if cerr := r.checkStartMethod(ctx, status.Account, requested); cerr != nil {
		return "", "", nil, false, cerr
	}
	return stateFirst, requested, nil, false, nil
}

// checkStartMethod holds a first factor a step-up names to what the account can
// prove. The password is only serviceable when the account holds one to prove
// again; a request that names it otherwise is the same bad request as one that
// names a method the deployment does not take in, refused before any record is
// written rather than as a failed proof later. The account checked is the one
// the step proves: the session's own account, which the operator is even under
// an impersonation.
func (r *routes) checkStartMethod(ctx context.Context, account authn.AccountID, requested string) error {
	if requested != methodPassword {
		return nil
	}
	hasPassword, err := r.accountHasPassword(ctx, account)
	if err != nil {
		return err
	}
	if !hasPassword {
		return fmt.Errorf("%w: the step-up names a password the account cannot prove", authn.ErrInvalidArgument)
	}
	return nil
}

// accountHasPassword reports whether the account can prove a password again:
// it is an enabled user account holding a password credential.
func (r *routes) accountHasPassword(ctx context.Context, account authn.AccountID) (bool, error) {
	acct, err := r.accounts.Get(ctx, account)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
	}
	return acct.Kind == authn.KindUser && acct.Status == authn.StatusActive && acct.PasswordSet, nil
}

// startRequirement checks the start requirement a purpose asks for. A link and
// an enrollment ask for the account's security requirement; a step-up asks for
// nothing beyond the identity the gateway stamped. It also reports whether that
// requirement was the fresh local multi-factor a factor-holding account asks
// for, which the flow records so its completion is held to the same one.
func (r *routes) startRequirement(c *call, id authn.Identity, purpose string) (requireMulti, ok bool) {
	if purpose != authn.PurposeLink && purpose != authn.PurposeEnroll {
		return false, true
	}
	requireMulti, err := r.accountHasActiveFactor(c.ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return false, false
	}
	req := r.requirementFor(requireMulti)
	if !id.Satisfies(req, r.clock.Now()) {
		c.stepUp(id, req)
		return false, false
	}
	return requireMulti, true
}

// applyEnroll drives a registration step through the multi-factor module. kind is
// the factor kind a first step names and is empty on a later one; fields are the
// request's own input, forwarded to the factor, whose step decides what it needs.
func (r *routes) applyEnroll(c *call, token, echo string, rec record, raw []byte, kind string, fields map[string]string) {
	// The multi-factor module makes the pending row active on the step that
	// completes a registration, and that step runs before the flow's completion
	// review. An impersonating flow therefore re-checks its record here, at the
	// entry to the step that can enable the factor: a record that lapsed in the
	// background -- by expiry, a disabled initiator or target, or a removed
	// membership -- does not land a factor on the account the flow was started
	// for.
	if rec.Impersonation != "" && !r.recordHolds(c, rec) {
		return
	}
	f := r.flowState(rec, token)
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: c.clientInfo()}
	// The registration's terminal audit row is written by the multi-factor
	// module from the context the step runs under. The answer route is public
	// and carries no identity, so an impersonating flow derives one from its
	// record here; a flow the account's own holder started keeps the anonymous
	// context its row has always had.
	ctx := c.ctx
	if rec.Impersonation != "" {
		ctx = r.flowAuditContext(c.ctx, rec)
	}
	got, ok := r.invoke(ctx, "enroll", func(cctx context.Context) (authn.Step, error) {
		return r.mfa.Enroll(cctx, f, authn.FactorKind(kind), in)
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
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, promptView(step.FieldsOf(got)), echo)
	case step.KindData:
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		r.replyStep(c, rec, stepView{Kind: nextWebAuthn, Options: step.DataOf(got)}, echo)
	case step.KindEnrolled:
		recalled := step.RecordOf(got)
		rec.MFA.Enrolled = recalled.ID
		rec.State = stateReady
		rec.MethodStep = ""
		rec.MethodState = nil
		if !r.swap(c, token, raw, rec) {
			return
		}
		writeComplete(c, rec)
	case step.KindRefused:
		// A registration step refused for a malformed input is a bad request,
		// not a credential that did not hold: a label outside its domain never
		// reached a factor to check.
		if errors.Is(step.CauseOf(got), authn.ErrInvalidArgument) {
			c.fail(mount.CodeBadRequest)
			return
		}
		rec.Answers++
		if !r.swap(c, token, raw, rec) {
			return
		}
		c.unauthorized(mount.CodeLoginFailed)
	case step.KindThrottled:
		c.throttled(step.RetryAfterOf(got))
	case step.KindUnavailable:
		c.unavailable()
	default:
		r.logger.Error(ModuleName + ": a factor gave an answer a registration step may not give")
		c.serverError()
	}
}

// checkFields holds one request's input to its bounds: at most maxInputFields
// fields, each at most maxBytes long. A start request's input is bounded by
// maxStartFieldBytes; a client's answer by maxAnswerFieldBytes, which is larger
// because an answer may carry a WebAuthn credential.
func checkFields(fields map[string]string, maxBytes int) error {
	if len(fields) > maxInputFields {
		return fmt.Errorf("%w: the input holds more than %d fields", authn.ErrInvalidArgument, maxInputFields)
	}
	for _, v := range fields {
		if len(v) > maxBytes {
			return fmt.Errorf("%w: an input field is longer than %d bytes", authn.ErrInvalidArgument, maxBytes)
		}
	}
	return nil
}

// admitSends is the read-only half of the source-address pre-throttle, checked
// at a request's entry before any method runs: a bucket already at its limit
// refuses the request with 429, so the call that would cross the limit sends
// nothing. It charges nothing itself; chargeSends adds what a call reports. A
// layer that is off (limit 0), an empty bucket and a counter that cannot be read
// all admit. A counter that cannot be read skips the layer and writes a Warn,
// because the layer is only a coarse source-address load shield and the
// authority is the failure back-off in the database.
func (r *routes) admitSends(c *call) bool {
	limit := r.settings.ipLimit()
	if limit <= 0 {
		return true
	}
	value, present, err := r.kv.Get(c.ctx, r.sourceBucketKey(c))
	if err != nil {
		r.logger.Warn(ModuleName+": the source-address pre-throttle could not read its count, so it is skipped",
			"cause", err.Error())
		return true
	}
	if !present {
		return true
	}
	count, err := strconv.ParseFloat(string(value), 64)
	if err != nil {
		// A value this layer did not write as a count: skip the layer rather
		// than guess at it, and say so.
		r.logger.Warn(ModuleName+": the source-address pre-throttle read a count it could not parse, so it is skipped",
			"cause", err.Error())
		return true
	}
	if count >= float64(limit) {
		c.throttled(0)
		return false
	}
	return true
}

// chargeSends charges the n verification messages one call sent against the
// request address's per-minute bucket and reports false, with the reply already
// set to throttled, when the count passes the limit. A call is charged for what
// it reports, so a call that sent nothing spends nothing and one that reports
// two sends spends two. A layer that is off (limit 0) charges nothing. A counter
// that cannot be written skips the layer and writes a Warn, as admitSends does.
func (r *routes) chargeSends(c *call, n int) bool {
	limit := r.settings.ipLimit()
	if limit <= 0 {
		return true
	}
	count, err := r.kv.IncrByFloatWithTTL(c.ctx, r.sourceBucketKey(c), float64(n), time.Minute)
	if err != nil {
		r.logger.Warn(ModuleName+": the source-address pre-throttle could not count, so it is skipped",
			"cause", err.Error())
		return true
	}
	if count > float64(limit) {
		c.throttled(0)
		return false
	}
	return true
}

// sourceBucketKey is one source address's one-minute message bucket.
func (r *routes) sourceBucketKey(c *call) string {
	return "authn:ip:" + c.clientIP + ":" + r.clock.Now().Format("200601021504")
}

// isSessionGone reports whether a session error means the session is not usable.
func isSessionGone(err error) bool {
	return errors.Is(err, authn.ErrSessionNotFound) || errors.Is(err, authn.ErrSessionRevoked)
}
