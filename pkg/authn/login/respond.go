package login

import (
	"context"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	nextChallenge = "challenge"
	nextRedirect  = "redirect"
	nextFactor    = "factor"
	nextEnroll    = "enroll"
	nextComplete  = "complete"
	nextWebAuthn  = "webauthn"
)

const stateAuthenticated = "authenticated"

const (
	stateLinked   = "linked"
	stateEnrolled = "enrolled"
)

const humanProviderOff = "off"

func (c *call) wire(body any) {
	if c.union {
		c.answer(body)
		return
	}
	c.json(http.StatusOK, body)
}

func (r *routes) writeFlow(c *call, rec record, flowToken string) {
	c.wire(flowReply{
		Seq:       rec.Seq,
		State:     rec.State,
		FlowToken: flowToken,
		Next:      r.nextOf(rec),
	})
}

func writeComplete(c *call, rec record) {
	c.wire(flowReply{Seq: rec.Seq, State: rec.State, Next: stepView{Kind: nextComplete}})
}

func writeSession(c *call, issued authn.Issued) {
	c.wire(sessionReply{
		State:   stateAuthenticated,
		Session: sessionView{ID: string(issued.ID), ExpiresAt: issued.ExpiresAt.UTC()},
	})
}

func writeToken(c *call, access authn.AccessToken, refresh authn.RefreshToken, now time.Time) {
	c.wire(tokenReply{
		State:        stateAuthenticated,
		TokenType:    "Bearer",
		AccessToken:  access.Value,
		ExpiresIn:    max(0, int(access.ExpiresAt.Sub(now).Seconds())),
		RefreshToken: refresh.Value,
	})
}

func writeAction(c *call, state string) {
	c.wire(actionReply{State: state})
}

func (r *routes) nextOf(rec record) stepView {
	switch rec.State {
	case stateReady:
		return stepView{Kind: nextComplete}
	case stateSignup:
		return signupStep(rec.Pending, rec.Method)
	case stateReprove:
		return stepView{Kind: nextChallenge}
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

func signupStep(pending *resolve.Pending, method string) stepView {
	view := stepView{Kind: nextChallenge}
	if pending == nil {
		view.Fields = signupFields("")
		return view
	}
	view.Fields = signupFields(pending.Source)
	if pending.Source == resolve.SourceSocial {
		view.ProvidedMethod = method
		view.ProvidedDisplayName = pending.DisplayName
		view.ProvidedEmail = pending.Email
	}
	return view
}

func signupFields(source resolve.Source) []fieldView {
	fields := []fieldView{{Name: fieldUsername, Kind: string(authn.FieldText), Required: true}}
	switch source {
	case resolve.SourceSocial:
		fields = append(fields, fieldView{Name: fieldPassword, Kind: string(authn.FieldPassword), Required: true, Secret: true})
	case resolve.SourcePhone:
		fields = append(fields,
			fieldView{Name: fieldDisplayName, Kind: string(authn.FieldText)},
			fieldView{Name: fieldPassword, Kind: string(authn.FieldPassword), Required: true, Secret: true})
	}
	return fields
}

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

func promptView(fields []authn.Field) stepView {
	return stepView{Kind: nextChallenge, Fields: fieldViews(fields)}
}

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
	signup := r.signupEnabled()
	password := r.passwordSignupEnabled()
	out := make([]methodDescriptorView, 0, len(r.methodNames))
	for _, name := range r.methodNames {
		if off[name] {
			continue
		}
		out = append(out, descriptorView(r.product.methods[name].Describe(), signup))
	}
	c.json(http.StatusOK, methodsReply{
		Methods: out,
		Signup: signupView{
			Enabled:  signup || password,
			Password: passwordSignupView{Enabled: password},
		},
		MFA:           mfaView{Factors: r.factorKinds()},
		Human:         r.humanGate(),
		PasswordReset: r.passwordResetCapability(disabled),
	})
	return c.result()
}

// humanGate reports the gate per surface; a nil gate reads as off, defence for a hand-built routes value.
func (r *routes) humanGate() humanView {
	if r.human == nil {
		off := humanSurfaceView{Provider: humanProviderOff}
		return humanView{Phone: off, Other: off}
	}
	return humanView{
		Phone: humanSurfaceGate(r.human, authn.HumanScopePhone),
		Other: humanSurfaceGate(r.human, authn.HumanScopeOther),
	}
}

func humanSurfaceGate(h authn.HumanVerifier, scope authn.HumanScope) humanSurfaceView {
	return humanSurfaceView{
		Required: h.Required(scope),
		Provider: h.Provider(scope),
		SiteKey:  h.SiteKey(scope),
		Action:   h.Action(scope),
	}
}

func descriptorView(d authn.MethodDescriptor, opensAccount bool) methodDescriptorView {
	return methodDescriptorView{
		Name:             d.Name,
		Kind:             string(d.Kind),
		Protocol:         string(d.Protocol),
		Label:            d.Label,
		OpensAccount:     d.OpensAccount && opensAccount,
		BindingProviders: d.BindingProviders,
		Fields:           fieldViews(d.Fields),
	}
}

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

func (r *routes) abandon(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	token, _, ok := r.readBindingToken(c, true)
	if !ok {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	rec, _, err := r.loadFlow(c.ctx, token)
	if err != nil {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	r.releaseFlow(c.ctx, rec, token)
	if err := r.deleteFlow(c.ctx, token); err != nil {
		r.fail(c, err)
		return c.result()
	}
	r.setFlowCookie(c, "", -1)
	c.empty(http.StatusNoContent)
	return c.result()
}
