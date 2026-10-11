package login

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// The two carriers a start may ask the flow token to travel in.
const (
	bindingCookie = "cookie"
	bindingHeader = "header"
)

// startRequest is the body of POST flows and POST flows/authenticated.
type startRequest struct {
	Method   string            `json:"method"`
	Purpose  string            `json:"purpose"`
	Delivery string            `json:"delivery"`
	ReturnTo string            `json:"return_to"`
	Binding  string            `json:"binding"`
	Input    map[string]string `json:"input"`
}

// startFlow begins a login flow. It reads no cookie, so a stale session cookie
// in the browser does not stand in the way of logging in.
func (r *routes) startFlow(c *gin.Context) {
	if !contentTypeJSON(c) {
		return
	}
	var req startRequest
	if !mount.Decode(c, &req) {
		return
	}
	ctx := c.Request.Context()
	if !r.ipAllow(c, ctx) {
		return
	}
	if _, ok := r.method(req.Method); !ok {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	disabled, err := r.settings.disabledMethods()
	if err != nil {
		r.fail(c, err)
		return
	}
	if slices.Contains(disabled, req.Method) {
		mount.Forbidden(c, mount.CodeMethodDisabled)
		return
	}
	delivery := req.Delivery
	if delivery == "" {
		delivery = deliverySession
	}
	if delivery != deliverySession && delivery != deliveryToken {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if !r.deliveryAllowed(delivery) || (delivery == deliveryToken && r.tokens == nil) {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if req.ReturnTo != "" {
		if verr := checkReturnTo(req.ReturnTo, r.trustedOrigins); verr != nil {
			r.fail(c, verr)
			return
		}
	}
	binding := req.Binding
	if binding == "" {
		binding = bindingCookie
	}
	if binding != bindingCookie && binding != bindingHeader {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if verr := checkFields(req.Input); verr != nil {
		r.fail(c, verr)
		return
	}

	token, err := newFlowToken()
	if err != nil {
		r.fail(c, err)
		return
	}
	now := r.clock.Now()
	ttl := r.settings.flowTTL()
	rec := record{
		Version:   recordVersion,
		Purpose:   authn.PurposeLogin,
		Method:    req.Method,
		State:     stateFirst,
		Binding:   tokenDigest(token),
		Delivery:  delivery,
		ReturnTo:  req.ReturnTo,
		Locale:    localeOf(c),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	raw, err := r.storeFlow(ctx, token, rec, ttl)
	if err != nil {
		r.fail(c, err)
		return
	}
	echo := ""
	if binding == bindingHeader {
		echo = token
	} else {
		r.setFlowCookie(c, token, int(ttl.Seconds()))
	}
	r.applyFirstBegin(c, token, echo, rec, raw, req.Input)
}

// startAuthenticated begins a link, a step-up or an enrollment on the identity
// the gateway authenticated.
func (r *routes) startAuthenticated(c *gin.Context) {
	if !contentTypeJSON(c) {
		return
	}
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		mount.Unauthorized(c, r.endpoint, "")
		return
	}
	if id.Impersonating() {
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
		return
	}
	var req startRequest
	if !mount.Decode(c, &req) {
		return
	}
	switch req.Purpose {
	case authn.PurposeLink, authn.PurposeStepUp, authn.PurposeEnroll:
	default:
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if err := checkFields(req.Input); err != nil {
		r.fail(c, err)
		return
	}
	if req.ReturnTo != "" {
		if err := checkReturnTo(req.ReturnTo, r.trustedOrigins); err != nil {
			r.fail(c, err)
			return
		}
	}
	status, err := r.sessions.StatusFresh(ctx, id.Session())
	if err != nil {
		if isSessionGone(err) {
			mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
			return
		}
		r.fail(c, err)
		return
	}
	if !status.Active || status.Account != id.Account() {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if !r.startRequirement(c, ctx, id, req.Purpose) {
		return
	}

	binding := req.Binding
	if binding == "" {
		binding = bindingCookie
	}
	if binding != bindingCookie && binding != bindingHeader {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	delivery := deliverySession
	if status.Kind == authn.SessionToken {
		delivery = deliveryToken
	}

	token, err := newFlowToken()
	if err != nil {
		r.fail(c, err)
		return
	}
	now := r.clock.Now()
	ttl := r.settings.flowTTL()
	rec := record{
		Version:   recordVersion,
		Purpose:   req.Purpose,
		State:     stateFirst,
		Binding:   tokenDigest(token),
		Account:   string(id.Account()),
		Session:   string(id.Session()),
		Delivery:  delivery,
		ReturnTo:  req.ReturnTo,
		Locale:    localeOf(c),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}

	var offered []factorView
	switch req.Purpose {
	case authn.PurposeLink:
		if _, ok := r.method(req.Method); !ok {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		rec.Method = req.Method
	case authn.PurposeEnroll:
		rec.State = stateEnroll
	case authn.PurposeStepUp:
		start, stepUpOffered, insufficient, serr := r.stepUpStart(ctx, status, req.Method)
		if serr != nil {
			r.fail(c, serr)
			return
		}
		if insufficient {
			mount.StepUpRequired(c, id, authn.FreshLocalMulti(r.settings.recentWindow()))
			return
		}
		rec.State = start
		rec.Method = req.Method
		rec.MFA.Required = start == stateSecond
		rec.MFA.Offered = stepUpOffered
		offered = stepUpOffered
	}

	raw, err := r.storeFlow(ctx, token, rec, ttl)
	if err != nil {
		r.fail(c, err)
		return
	}
	echo := ""
	if binding == bindingHeader {
		echo = token
	} else {
		r.setFlowCookie(c, token, int(ttl.Seconds()))
	}

	switch rec.State {
	case stateFirst:
		r.applyFirstBegin(c, token, echo, rec, raw, req.Input)
	case stateEnroll:
		r.applyEnroll(c, token, echo, rec, raw, req.Input["kind"], req.Input)
	default: // second
		r.replyStep(c, rec, stepView{Kind: nextFactor, Factors: offered}, echo)
	}
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
// nothing beyond the identity the gateway stamped.
func (r *routes) startRequirement(c *gin.Context, ctx context.Context, id authn.Identity, purpose string) bool {
	if purpose != authn.PurposeLink && purpose != authn.PurposeEnroll {
		return true
	}
	req, err := r.accountRequirement(ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return false
	}
	if !id.Satisfies(req, r.clock.Now()) {
		mount.StepUpRequired(c, id, req)
		return false
	}
	return true
}

// applyEnroll drives a registration step through the multi-factor module. kind is
// the factor kind a first step names and is empty on a later one; fields are the
// request's own input, forwarded to the factor, whose step decides what it needs.
func (r *routes) applyEnroll(c *gin.Context, token, echo string, rec record, raw []byte, kind string, fields map[string]string) {
	ctx := c.Request.Context()
	f := r.flowState(rec, token)
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: requestClient(c)}
	got, ok := r.invoke(ctx, "enroll", func(cctx context.Context) (authn.Step, error) {
		return r.mfa.Enroll(cctx, f, authn.FactorKind(kind), in)
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
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	case step.KindThrottled:
		mount.Throttled(c, step.RetryAfterOf(got))
	case step.KindUnavailable:
		mount.Unavailable(c)
	default:
		r.logger.Error(ModuleName + ": a factor gave an answer a registration step may not give")
		serverError(c)
	}
}

// checkFields holds the start input to its bounds.
func checkFields(fields map[string]string) error {
	if len(fields) > maxStartFields {
		return fmt.Errorf("%w: the input holds more than %d fields", authn.ErrInvalidArgument, maxStartFields)
	}
	for _, v := range fields {
		if len(v) > maxFieldBytes {
			return fmt.Errorf("%w: an input field is longer than %d bytes", authn.ErrInvalidArgument, maxFieldBytes)
		}
	}
	return nil
}

// localeOf maps the request's Accept-Language to the closed set the SMS
// templates use. An empty result reads as zh-CN.
func localeOf(c *gin.Context) string {
	header := c.GetHeader("Accept-Language")
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" {
			continue
		}
		primary := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
		switch primary {
		case "en":
			return "en-US"
		case "zh":
			return "zh-CN"
		default:
			return ""
		}
	}
	return ""
}

// ipAllow applies the source-address pre-throttle when it is on. A counter that
// cannot be read skips the layer and writes a Warn, because the layer is only a
// coarse pre-throttle and the authority is the failure back-off in the database.
func (r *routes) ipAllow(c *gin.Context, ctx context.Context) bool {
	limit := r.settings.ipLimit()
	if limit <= 0 {
		return true
	}
	key := "authn:ip:" + c.ClientIP() + ":" + r.clock.Now().Format("200601021504")
	count, err := r.kv.IncrByFloatWithTTL(ctx, key, 1, time.Minute)
	if err != nil {
		r.logger.Warn(ModuleName+": the source-address pre-throttle could not count, so it is skipped",
			"cause", err.Error())
		return true
	}
	if count > float64(limit) {
		mount.Throttled(c, 0)
		return false
	}
	return true
}

// isSessionGone reports whether a session error means the session is not usable.
func isSessionGone(err error) bool {
	return errors.Is(err, authn.ErrSessionNotFound) || errors.Is(err, authn.ErrSessionRevoked)
}
