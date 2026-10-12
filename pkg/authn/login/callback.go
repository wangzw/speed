package login

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// callback handles the redirect an external party sends the user agent back
// with. The engine moves the flow to processing before the method is asked, so
// that a replayed authorization code or assertion loses the compare-and-swap and
// is answered an invalid flow.
func (r *routes) callback(c *gin.Context) {
	provider := c.Param("provider")
	ctx := c.Request.Context()
	token, _, present := r.readBindingToken(c, false)
	if !present {
		r.callbackIDPInitiated(c, provider)
		return
	}
	rec, raw, err := r.loadFlow(ctx, token)
	if err != nil {
		r.fail(c, err)
		return
	}
	// A callback only needs the flow to exist and to be at the first factor: a
	// link, a step-up and an enrollment started on POST flows/authenticated with
	// a redirect method land their provider's callback on this very route.
	if rec.State != stateFirst {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	// The path's provider must be the one the flow recorded before the method is
	// asked: a social method records its own name, an enterprise provider records
	// the identifier the start input named. A callback aimed at another provider
	// is refused and never reaches a method.
	if rec.Provider != provider {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	m, ok := r.method(rec.Method)
	if !ok {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if !r.beginProcessing(c, token, rec, raw) {
		return
	}
	r.answerCallback(c, token, rec, m)
}

// beginProcessing swaps a first-state flow to processing. A false result means
// another callback already claimed it, or the record moved, and the request is
// answered an invalid flow.
func (r *routes) beginProcessing(c *gin.Context, token string, rec record, raw []byte) bool {
	proc := rec
	proc.State = stateProcessing
	return r.swap(c, token, raw, proc)
}

// answerCallback runs the method's answer to a callback and moves the record out
// of processing. A refusal, an outage or an unresolved account leaves the flow a
// tombstone: the authorization code or assertion is spent and the user starts
// again.
func (r *routes) answerCallback(c *gin.Context, token string, rec record, m authn.LoginMethod) {
	ctx := c.Request.Context()
	proc := rec
	proc.State = stateProcessing
	procBytes, err := json.Marshal(proc)
	if err != nil {
		serverError(c)
		return
	}
	form, ok := formValues(c)
	if !ok {
		// The posted body is over the callback's limit. It is refused before any
		// method reads it, and the flow is left a tombstone.
		failed := proc
		failed.State = stateFailed
		if !r.swap(c, token, procBytes, failed) {
			return
		}
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
		return
	}
	f := r.flowState(proc, token)
	in := authn.StepInput{
		Action: authn.InputCallback,
		Query:  c.Request.URL.Query(),
		Form:   form,
		Client: requestClient(c),
	}
	got, ok := r.invoke(ctx, proc.Method, func(cctx context.Context) (authn.Step, error) {
		return m.Answer(cctx, f, in)
	})
	if !ok {
		serverError(c)
		return
	}
	r.migrateCallback(c, token, procBytes, proc, got)
}

// migrateCallback turns a callback's answer into the next record state. The old
// bytes are those of the processing record, so the second compare-and-swap
// matches what the first one wrote.
func (r *routes) migrateCallback(c *gin.Context, token string, old []byte, rec record, got authn.Step) {
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
		r.reportLogin(c.Request.Context(), rec, outcomeFailed)
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	case step.KindUnavailable:
		rec.State = stateFailed
		if !r.swap(c, token, old, rec) {
			return
		}
		r.reportLogin(c.Request.Context(), rec, outcomeUnavailable)
		mount.Unavailable(c)
	default:
		rec.State = stateFailed
		if !r.swap(c, token, old, rec) {
			return
		}
		r.logger.Error(ModuleName + ": a login method gave an answer a callback may not give")
		serverError(c)
	}
}

// callbackIDPInitiated answers a callback that carries no flow cookie and comes
// from an enterprise provider that allows an identity-provider-initiated
// sign-in. The engine creates a login flow on the spot, moves it to processing
// and answers it like any other callback. Without a binding this path cannot
// defend against a login CSRF, which is why the provider row leaves it off by
// default.
func (r *routes) callbackIDPInitiated(c *gin.Context, provider string) {
	ctx := c.Request.Context()
	if c.Request.Method != http.MethodPost || r.sso == nil {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	row, err := r.sso.Get(ctx, provider)
	if err != nil {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if !row.AllowIDPInitiated || !row.Enabled {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	m, ok := r.method(row.Protocol)
	if !ok {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	ret := r.settings.defaultReturnTo()
	if verr := checkReturnTo(ret, r.trustedOrigins); verr != nil {
		ret = "/"
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
		Method:    row.Protocol,
		Provider:  provider,
		State:     stateProcessing,
		Binding:   tokenDigest(token),
		Delivery:  deliverySession,
		ReturnTo:  ret,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if _, err := r.storeFlow(ctx, token, rec, ttl); err != nil {
		r.fail(c, err)
		return
	}
	r.setFlowCookie(c, token, int(ttl.Seconds()))
	r.answerCallback(c, token, rec, m)
}

// maxCallbackFormBytes is the bound on a callback's posted body, before the form
// is parsed. The design gives SAML's ACS a 512 KiB body limit; the bound is
// applied here, so an oversized POST is refused rather than read.
const maxCallbackFormBytes = 512 << 10

// formValues reads a posted form without consuming a JSON body, for SAML's POST
// binding. The body is bounded before it is parsed; the second result is false
// when the body is over the limit or does not parse, which the caller answers as
// a refusal rather than handing a method a form it cannot trust.
func formValues(c *gin.Context) (map[string][]string, bool) {
	if c.Request.Method != http.MethodPost {
		return nil, true
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCallbackFormBytes)
	if err := c.Request.ParseForm(); err != nil {
		return nil, false
	}
	return c.Request.PostForm, true
}
