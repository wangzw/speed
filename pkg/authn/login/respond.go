package login

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
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

// flowReply is the answer to a step that keeps the flow open. flow_token is set
// only for a header-bound start.
type flowReply struct {
	Seq       int      `json:"seq"`
	State     string   `json:"state"`
	FlowToken string   `json:"flow_token,omitempty"`
	Next      stepView `json:"next"`
}

// stepView is what the client does next.
type stepView struct {
	Kind        string       `json:"kind"`
	Fields      []fieldView  `json:"fields,omitempty"`
	ExpiresIn   int          `json:"expires_in,omitempty"`
	ResendAfter int          `json:"resend_after,omitempty"`
	URL         string       `json:"url,omitempty"`
	Method      string       `json:"method,omitempty"`
	Factors     []factorView `json:"factors,omitempty"`
	Kinds       []string     `json:"kinds,omitempty"`
	Options     any          `json:"options,omitempty"`
}

// fieldView is one input the client is asked for.
type fieldView struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
}

// sessionReply is the body of a completed login delivered as a cookie.
type sessionReply struct {
	State   string      `json:"state"`
	Session sessionView `json:"session"`
}

// sessionView is the public facts of a session.
type sessionView struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
}

// tokenReply is the body of a completed login delivered as a token pair.
type tokenReply struct {
	State        string `json:"state"`
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// methodDescriptorView is one login method in the discovery document.
type methodDescriptorView struct {
	Name         string      `json:"name"`
	Kind         string      `json:"kind"`
	Protocol     string      `json:"protocol,omitempty"`
	Label        string      `json:"label,omitempty"`
	OpensAccount bool        `json:"opens_account,omitempty"`
	Fields       []fieldView `json:"fields,omitempty"`
}

// signupView says whether the deployment lets a first login open an account.
type signupView struct {
	Enabled bool `json:"enabled"`
}

// mfaView is the kinds of second factor the deployment takes in.
type mfaView struct {
	Factors []string `json:"factors"`
}

// methodsReply is the body of the methods listing: the descriptors of the
// methods taking part that are not turned off, whether registration is open, and
// the kinds of second factor the deployment offers.
type methodsReply struct {
	Methods []methodDescriptorView `json:"methods"`
	Signup  signupView             `json:"signup"`
	MFA     mfaView                `json:"mfa"`
}

// writeFlow writes a reply that keeps a flow open. flowToken is echoed only for
// a request that carried it in the Authn-Flow header.
func (r *routes) writeFlow(c *gin.Context, rec record, flowToken string) {
	c.JSON(http.StatusOK, flowReply{
		Seq:       rec.Seq,
		State:     rec.State,
		FlowToken: flowToken,
		Next:      r.nextOf(rec),
	})
}

// writeComplete writes the reply of a step that has reached ready.
func writeComplete(c *gin.Context, rec record) {
	c.JSON(http.StatusOK, flowReply{Seq: rec.Seq, State: rec.State, Next: stepView{Kind: nextComplete}})
}

// writeSession writes the body of a completed login delivered as a cookie.
func writeSession(c *gin.Context, issued authn.Issued) {
	c.JSON(http.StatusOK, sessionReply{
		State:   stateAuthenticated,
		Session: sessionView{ID: string(issued.ID), ExpiresAt: rfc3339(issued.ExpiresAt)},
	})
}

// writeToken writes the body of a completed login delivered as a token pair.
func writeToken(c *gin.Context, access authn.AccessToken, refresh authn.RefreshToken, now time.Time) {
	expiresIn := int(access.ExpiresAt.Sub(now).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	c.JSON(http.StatusOK, tokenReply{
		State:        stateAuthenticated,
		TokenType:    "Bearer",
		AccessToken:  access.Value,
		ExpiresIn:    expiresIn,
		RefreshToken: refresh.Value,
	})
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

// methods answers GET B/methods with the descriptors of the methods taking part
// that are not turned off, in the order of their names, together with whether
// registration is open and the kinds of second factor the deployment offers.
func (r *routes) methods(c *gin.Context) {
	disabled, err := r.settings.disabledMethods()
	if err != nil {
		r.fail(c, err)
		return
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
	c.JSON(http.StatusOK, methodsReply{
		Methods: out,
		Signup:  signupView{Enabled: r.signup},
		MFA:     mfaView{Factors: r.factorKinds()},
	})
}

// descriptorView copies a method's descriptor into the wire shape.
func descriptorView(d authn.MethodDescriptor) methodDescriptorView {
	return methodDescriptorView{
		Name:         d.Name,
		Kind:         string(d.Kind),
		Protocol:     string(d.Protocol),
		Label:        d.Label,
		OpensAccount: d.OpensAccount,
		Fields:       fieldViews(d.Fields),
	}
}

// current answers GET B/flows/current with the state of the flow the request is
// bound to, so that a page refresh can resume.
func (r *routes) current(c *gin.Context) {
	_, echo, rec, _, ok := r.requireBinding(c, true)
	if !ok {
		return
	}
	switch rec.State {
	case stateDone, stateFailed:
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	r.writeFlow(c, rec, echo)
}

// abandon answers DELETE B/flows/current by removing the flow the request is
// bound to. It is idempotent: a request with no binding or with a binding to a
// flow that is gone is the same 401 flow-invalid, and a request that removes a
// live flow answers 204.
func (r *routes) abandon(c *gin.Context) {
	token, _, ok := r.readBindingToken(c, true)
	if !ok {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if _, _, err := r.loadFlow(c.Request.Context(), token); err != nil {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if err := r.deleteFlow(c.Request.Context(), token); err != nil {
		r.fail(c, err)
		return
	}
	r.setFlowCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}
