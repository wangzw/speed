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

const (
	bindingCookie = "cookie"
	bindingHeader = "header"
)

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

func (r *routes) stepUpStart(ctx context.Context, status authn.SessionStatus, requirement authn.AccountID, requested, excluded string) (start, method string, offered []factorView, insufficient bool, err error) {
	firstProof := authn.Proof{Methods: status.Methods}
	outcome, factors, err := r.mfa.Require(ctx, status.Account, requirement, firstProof)
	if err != nil {
		return "", "", nil, false, err
	}
	if outcome == authn.MFAFactor {
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
			if f.ActivatedAt.Before(status.AuthTime) {
				usable = append(usable, f)
			}
		}
		if len(usable) > 0 {
			return stateSecond, "", factorViews(usable), false, nil
		}
		if len(factors) == 0 {
			return "", "", nil, true, nil
		}
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
	if !r.stepUpChannel(status, requested) {
		return "", "", nil, false, fmt.Errorf("%w: the step-up names a method the session was not established with", authn.ErrInvalidArgument)
	}
	if cerr := r.checkStartMethod(ctx, status.Account, requested); cerr != nil {
		return "", "", nil, false, cerr
	}
	return stateFirst, requested, nil, false, nil
}

func (r *routes) stepUpChannel(status authn.SessionStatus, name string) bool {
	switch name {
	case methodPassword:
		return slices.Contains(status.Methods, authn.MethodPassword)
	case methodSMS:
		return slices.Contains(status.Methods, authn.MethodSMS)
	}
	method, ok := r.method(name)
	if !ok || method.Describe().Kind != authn.MethodKindRedirect {
		return false
	}
	return slices.ContainsFunc(status.Methods, func(m authn.Method) bool {
		switch m {
		case authn.MethodOAuth, authn.MethodOIDC, authn.MethodSAML:
			return true
		}
		return false
	})
}

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

func (r *routes) applyEnroll(c *call, token, echo string, rec record, raw []byte, kind string, fields map[string]string) {
	// An impersonating flow re-checks its record at the entry to the step that can enable the factor, which runs before the completion review.
	if rec.Impersonation != "" && !r.recordHolds(c, rec) {
		return
	}
	f := r.flowState(rec, token)
	in := authn.StepInput{Action: authn.InputSubmit, Fields: fields, Client: c.clientInfo()}
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
		if rec.MFA.EnrollMulti {
			rec.State = stateReprove
			rec.Method = methodPassword
			rec.MethodStep = ""
			rec.MethodState = nil
			r.applyFirstBegin(c, token, echo, rec, raw, nil, "")
			return
		}
		rec.State = stateReady
		rec.MethodStep = ""
		rec.MethodState = nil
		if !r.swap(c, token, raw, rec) {
			return
		}
		writeComplete(c, rec)
	case step.KindRefused:
		if errors.Is(step.CauseOf(got), authn.ErrInvalidArgument) {
			c.fail(mount.CodeBadRequest)
			return
		}
		if errors.Is(step.CauseOf(got), authn.ErrPhoneRequired) {
			c.forbidden(mount.CodePhoneRequired)
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

func (r *routes) sourceBucketKey(c *call) string {
	return "authn:ip:" + c.clientIP + ":" + r.clock.Now().Format("200601021504")
}

func isSessionGone(err error) bool {
	return errors.Is(err, authn.ErrSessionNotFound) || errors.Is(err, authn.ErrSessionRevoked)
}
