package login

import (
	"context"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The kinds of a next step, the closed set the client reads out of next.kind.
const (
	nextChallenge = "challenge"
	nextRedirect  = "redirect"
	nextFactor    = "factor"
	nextEnroll    = "enroll"
	nextComplete  = "complete"
	nextWebAuthn  = "webauthn"
)

// The states a completed login answers with.
const stateAuthenticated = "authenticated"

// The states a flow that ends without a session answers with.
const (
	stateLinked   = "linked"
	stateEnrolled = "enrolled"
)

// wire writes a body as the answer route's union when the call is the answer
// route, and as the body itself otherwise.
func (c *call) wire(body any) {
	if c.union {
		c.answer(body)
		return
	}
	c.json(http.StatusOK, body)
}

// writeFlow writes a reply that keeps a flow open. flowToken is echoed only for
// a request that carried it in the Authn-Flow header.
func (r *routes) writeFlow(c *call, rec record, flowToken string) {
	c.wire(flowReply{
		Seq:       rec.Seq,
		State:     rec.State,
		FlowToken: flowToken,
		Next:      r.nextOf(rec),
	})
}

// writeComplete writes the reply of a step that has reached ready.
func writeComplete(c *call, rec record) {
	c.wire(flowReply{Seq: rec.Seq, State: rec.State, Next: stepView{Kind: nextComplete}})
}

// writeSession writes the body of a completed login delivered as a cookie.
func writeSession(c *call, issued authn.Issued) {
	c.wire(sessionReply{
		State:   stateAuthenticated,
		Session: sessionView{ID: string(issued.ID), ExpiresAt: rfc3339(issued.ExpiresAt)},
	})
}

// writeToken writes the body of a completed login delivered as a token pair.
func writeToken(c *call, access authn.AccessToken, refresh authn.RefreshToken, now time.Time) {
	c.wire(tokenReply{
		State:        stateAuthenticated,
		TokenType:    "Bearer",
		AccessToken:  access.Value,
		ExpiresIn:    max(0, int(access.ExpiresAt.Sub(now).Seconds())),
		RefreshToken: refresh.Value,
	})
}

// writeAction writes the body of a flow that ended by linking an identity or
// registering a factor.
func writeAction(c *call, state string) {
	c.wire(actionReply{State: state})
}

// nextOf is what a client does next given where the flow stands. A flow refreshed
// with GET flows/current reads this, so it is built from the record alone: a
// first-factor step the method named cannot be re-prompted with its fields, which
// the method decides on the next answer. The enroll step names the factor kinds
// the assembly takes in, so the client can choose one.
func (r *routes) nextOf(rec record) stepView {
	switch rec.State {
	case stateReady:
		return stepView{Kind: nextComplete}
	case stateSecond:
		return stepView{Kind: nextFactor, Factors: rec.MFA.Offered}
	case stateEnroll:
		return stepView{Kind: nextEnroll, Kinds: r.factorKinds()}
	case stateDone:
		return stepView{Kind: nextComplete}
	default:
		return stepView{Kind: nextChallenge}
	}
}

// factorKinds renders the factor kinds the assembly takes in, for the enroll
// next step and for the discovery listing. A module with none, which cannot
// reach here, offers none.
func (r *routes) factorKinds() []string {
	if r.mfa == nil {
		return nil
	}
	kinds := r.mfa.Kinds()
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

// promptView is the next step of a method's prompt answer.
func promptView(fields []authn.Field) stepView {
	return stepView{Kind: nextChallenge, Fields: fieldViews(fields)}
}

// fieldViews copies the fields an answer asks for into the wire shape.
func fieldViews(fields []authn.Field) []fieldView {
	if len(fields) == 0 {
		return nil
	}
	out := make([]fieldView, len(fields))
	for i, f := range fields {
		out[i] = fieldView{Name: f.Name, Kind: f.Kind, Required: f.Required, Secret: f.Secret}
	}
	return out
}

// rfc3339 is a time as the wire carries an instant. A zero time is written empty.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// methods answers GET flows with the descriptors of the methods taking part
// that are not turned off, in the order of their names, together with whether
// registration is open and the kinds of second factor the deployment offers.
func (r *routes) methods(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	disabled, err := r.settings.disabledMethods()
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	off := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		off[name] = true
	}
	out := make([]methodDescriptorView, 0, len(r.methodNames))
	for _, name := range r.methodNames {
		if off[name] {
			continue
		}
		out = append(out, descriptorView(r.product.methods[name].Describe()))
	}
	c.json(http.StatusOK, methodsReply{
		Methods: out,
		Signup:  signupView{Enabled: r.signup},
		MFA:     mfaView{Factors: r.factorKinds()},
	})
	return c.result()
}

// descriptorView copies a method's descriptor into the wire shape.
func descriptorView(d authn.MethodDescriptor) methodDescriptorView {
	return methodDescriptorView{
		Name:             d.Name,
		Kind:             string(d.Kind),
		Protocol:         string(d.Protocol),
		Label:            d.Label,
		OpensAccount:     d.OpensAccount,
		BindingProviders: d.BindingProviders,
		Fields:           fieldViews(d.Fields),
	}
}

// current answers GET flows/current with the state of the flow the request is
// bound to, so that a page refresh can resume.
func (r *routes) current(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	_, echo, rec, _, ok := r.requireBinding(c, true)
	if !ok {
		return c.result()
	}
	switch rec.State {
	case stateDone, stateFailed:
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	r.writeFlow(c, rec, echo)
	return c.result()
}

// abandon answers DELETE flows/current by removing the flow the request is
// bound to. It is idempotent: a request with no binding or with a binding to a
// flow that is gone is the same 401 flow-invalid, and a request that removes a
// live flow answers 204.
func (r *routes) abandon(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	token, _, ok := r.readBindingToken(c, true)
	if !ok {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if _, _, err := r.loadFlow(c.ctx, token); err != nil {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if err := r.deleteFlow(c.ctx, token); err != nil {
		r.fail(c, err)
		return c.result()
	}
	r.setFlowCookie(c, "", -1)
	c.empty(http.StatusNoContent)
	return c.result()
}
