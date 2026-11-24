package login

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func (r *routes) callbackGet(ctx context.Context, in callbackInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	reply, err := r.callback(c, in.Provider)
	return r.landCallbackFailure(c, reply, err)
}

func (r *routes) callbackPost(ctx context.Context, in callbackInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	reply, err := r.bounce(c, in.Provider)
	return r.landCallbackFailure(c, reply, err)
}

func (r *routes) callbackResume(ctx context.Context, in callbackInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	reply, err := r.callback(c, in.Provider)
	return r.landCallbackFailure(c, reply, err)
}

const landingFragment = "authn_error"

const landingEmailKey = "authn_email"

func (r *routes) landCallbackFailure(c *call, reply speedhttp.Reply, err error) (speedhttp.Reply, error) {
	if err == nil || !c.navigation() {
		return reply, err
	}
	landing := c.failureLanding
	if landing == "" {
		landing = r.settings.callbackErrorReturnTo()
	}
	if checkReturnTo(landing, r.trustedOrigins) != nil {
		return reply, err
	}
	if i := strings.IndexByte(landing, '#'); i >= 0 {
		landing = landing[:i]
	}
	c.err = nil
	c.redirect(http.StatusSeeOther, landingTarget(landing, c.failureEmail, err))
	return c.result()
}

func maskEmail(address string) string {
	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(local)
	return string(first) + "***@" + domain
}

func landingTarget(landing, email string, err error) string {
	target := landing + "#" + landingFragment + "=" + url.QueryEscape(problemCode(err))
	if email != "" {
		target += "&" + landingEmailKey + "=" + url.QueryEscape(email)
	}
	return target
}

func problemCode(err error) string {
	var fault speedhttp.Fault
	if errors.As(err, &fault) {
		if code, ok := strings.CutPrefix(fault.Type, typePrefix); ok && code != "" {
			return code
		}
	}
	return mount.CodeUnavailable
}

func (r *routes) callback(c *call, provider string) (speedhttp.Reply, error) {
	token, _, present := r.readBindingToken(c, false)
	if !present {
		r.callbackIDPInitiated(c, provider)
		return c.result()
	}
	rec, raw, err := r.loadFlow(c.ctx, token)
	posted := c.req.Method == http.MethodPost
	switch {
	case posted && errors.Is(err, authn.ErrFlowInvalid):
		r.callbackIDPInitiated(c, provider)
		return c.result()
	case err != nil:
		r.fail(c, err)
		return c.result()
	}
	if posted && (rec.State != stateFirst || rec.Provider != provider) {
		r.callbackIDPInitiated(c, provider)
		return c.result()
	}
	if rec.Purpose != authn.PurposeLogin {
		c.failureLanding = r.returnTarget(rec)
	}
	if rec.State != stateFirst {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if rec.Provider != provider {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	m, ok := r.method(rec.Method)
	if !ok {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if !r.beginProcessing(c, token, rec, raw) {
		return c.result()
	}
	r.answerCallback(c, token, rec, m)
	return c.result()
}

func (r *routes) beginProcessing(c *call, token string, rec record, raw []byte) bool {
	proc := rec
	proc.State = stateProcessing
	return r.swap(c, token, raw, proc)
}

func (r *routes) answerCallback(c *call, token string, rec record, m authn.LoginMethod) {
	proc := rec
	proc.State = stateProcessing
	procBytes, err := json.Marshal(proc)
	if err != nil {
		c.serverError()
		return
	}
	form, ok := formValues(c)
	if !ok {
		failed := proc
		failed.State = stateFailed
		if !r.swap(c, token, procBytes, failed) {
			return
		}
		c.unauthorized(mount.CodeLoginFailed)
		return
	}
	f := r.flowState(proc, token)
	in := authn.StepInput{
		Action: authn.InputCallback,
		Query:  c.req.URL.Query(),
		Form:   form,
		Client: c.clientInfo(),
	}
	got, ok := r.invoke(c.ctx, proc.Method, func(cctx context.Context) (authn.Step, error) {
		return m.Answer(cctx, f, in)
	})
	if !ok {
		c.serverError()
		return
	}
	r.migrateCallback(c, token, procBytes, proc, got)
}

func (r *routes) migrateCallback(c *call, token string, old []byte, rec record, got authn.Step) {
	if !r.recordSends(c, token, old, &rec, got) {
		return
	}
	switch step.KindOf(got) {
	case step.KindPrompt:
		rec.State = stateFirst
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, old, rec) {
			return
		}
		r.replyStep(c, rec, promptView(step.FieldsOf(got)), "")
	case step.KindRedirect:
		rec.State = stateFirst
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, old, rec) {
			return
		}
		r.replyStep(c, rec, stepView{Kind: nextRedirect, URL: step.RedirectOf(got), Method: "GET"}, "")
	case step.KindData:
		rec.State = stateFirst
		rec.MethodStep = step.NameOf(got)
		rec.MethodState = step.PrivateOf(got)
		rec.Seq++
		rec.Answers++
		if !r.swap(c, token, old, rec) {
			return
		}
		r.replyStep(c, rec, stepView{Kind: nextWebAuthn, Options: step.DataOf(got)}, "")
	case step.KindProven:
		r.resolveAndDecide(c, token, "", rec, old, step.ProofOf(got), true)
	case step.KindRefused:
		rec.State = stateFailed
		if !r.swap(c, token, old, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, outcomeFailed)
		c.unauthorized(mount.CodeLoginFailed)
	case step.KindUnavailable:
		rec.State = stateFailed
		if !r.swap(c, token, old, rec) {
			return
		}
		r.reportLogin(c.ctx, rec, outcomeUnavailable)
		c.unavailable()
	default:
		rec.State = stateFailed
		if !r.swap(c, token, old, rec) {
			return
		}
		r.logger.Error(ModuleName + ": a login method gave an answer a callback may not give")
		c.serverError()
	}
}

func (r *routes) callbackIDPInitiated(c *call, provider string) {
	if c.req.Method != http.MethodPost || r.sso == nil {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	row, err := r.sso.Get(c.ctx, provider)
	if err != nil {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	if !row.AllowIDPInitiated || !row.Enabled {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	m, ok := r.method(row.Protocol)
	if !ok {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	ret := r.defaultReturnTarget()
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
		Method:    row.Protocol,
		Provider:  provider,
		State:     stateProcessing,
		Binding:   tokenDigest(token),
		Delivery:  deliverySession,
		ReturnTo:  ret,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if _, err := r.storeFlow(c.ctx, token, rec, ttl); err != nil {
		r.fail(c, err)
		return
	}
	r.answerCallback(c, token, rec, m)
	if c.err == nil {
		r.setFlowCookie(c, token, int(ttl.Seconds()))
	}
}

func (r *routes) defaultReturnTarget() string {
	ret := r.settings.defaultReturnTo()
	if verr := checkReturnTo(ret, r.trustedOrigins); verr != nil {
		return "/"
	}
	return ret
}

const maxCallbackFormBytes = 512 << 10

func formValues(c *call) (map[string][]string, bool) {
	if c.req.Method != http.MethodPost {
		return nil, true
	}
	c.req.Body = http.MaxBytesReader(c.w, c.req.Body, maxCallbackFormBytes)
	if err := c.req.ParseForm(); err != nil {
		return nil, false
	}
	return c.req.PostForm, true
}
