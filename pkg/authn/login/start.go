package login

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	if !r.ipAllow(c) {
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
	r.applyFirstBegin(c, token, echo, rec, raw, in.Input)
	return c.result()
}

// startAuthenticated begins a link, a step-up or an enrollment on the identity
// the gateway authenticated.
func (r *routes) startAuthenticated(ctx context.Context, in startAuthenticatedInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	id, ok := authn.FromContext(c.ctx)
	if !ok || id.Account() == "" {
		c.unauthorized("")
		return c.result()
	}
	if id.Impersonating() {
		c.forbidden(mount.CodeImpersonationRestricted)
		return c.result()
	}
	switch in.Purpose {
	case authn.PurposeLink, authn.PurposeStepUp, authn.PurposeEnroll:
	default:
		c.fail(mount.CodeBadRequest)
		return c.result()
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
	if !status.Active || status.Account != id.Account() {
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
		start, stepUpOffered, insufficient, serr := r.stepUpStart(c.ctx, status, in.Method)
		if serr != nil {
			r.fail(c, serr)
			return c.result()
		}
		if insufficient {
			c.stepUp(id, authn.FreshLocalMulti(r.settings.recentWindow()))
			return c.result()
		}
		rec.State = start
		rec.Method = in.Method
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
		r.applyFirstBegin(c, token, echo, rec, raw, in.Input)
	case stateEnroll:
		r.applyEnroll(c, token, echo, rec, raw, in.Input["kind"], in.Input)
	default: // second
		r.replyStep(c, rec, stepView{Kind: nextFactor, Factors: offered}, echo)
	}
	return c.result()
}

// stepUpStart decides where a step-up flow starts. It asks the policy with a
// proof made of the session's methods; a usable factor keeps the flow at the
// second factor, every other outcome starts it at the first factor with the
// method the request named. insufficient reports that the account has factors
// but none that may strengthen this session, which is the flow's refusal.
func (r *routes) stepUpStart(ctx context.Context, status authn.SessionStatus, method string) (start string, offered []factorView, insufficient bool, err error) {
	first := authn.Proof{Methods: status.Methods}
	outcome, factors, err := r.mfa.Require(ctx, status.Account, first)
	if err != nil {
		return "", nil, false, err
	}
	if outcome == authn.MFAFactor {
		usable := make([]authn.FactorRecord, 0, len(factors))
		for _, f := range factors {
			// A factor activated at or after the session's authentication time
			// was registered during that login and may not strengthen it.
			if f.ActivatedAt.Before(status.AuthTime) {
				usable = append(usable, f)
			}
		}
		if len(usable) == 0 {
			return "", nil, true, nil
		}
		return stateSecond, factorViews(usable), false, nil
	}
	if _, ok := r.method(method); !ok {
		return "", nil, false, fmt.Errorf("%w: the step-up names a method not taken in", authn.ErrInvalidArgument)
	}
	return stateFirst, nil, false, nil
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
	f := r.flowState(rec, token)
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: c.clientInfo()}
	got, ok := r.invoke(c.ctx, "enroll", func(cctx context.Context) (authn.Step, error) {
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
		rec.Seq++
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

// ipAllow applies the source-address pre-throttle when it is on. A counter that
// cannot be read skips the layer and writes a Warn, because the layer is only a
// coarse pre-throttle and the authority is the failure back-off in the database.
func (r *routes) ipAllow(c *call) bool {
	limit := r.settings.ipLimit()
	if limit <= 0 {
		return true
	}
	key := "authn:ip:" + c.clientIP + ":" + r.clock.Now().Format("200601021504")
	count, err := r.kv.IncrByFloatWithTTL(c.ctx, key, 1, time.Minute)
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

// isSessionGone reports whether a session error means the session is not usable.
func isSessionGone(err error) bool {
	return errors.Is(err, authn.ErrSessionNotFound) || errors.Is(err, authn.ErrSessionRevoked)
}
