package login

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
)

func statusIdentity(st authn.SessionStatus) authn.Identity {
	return identity.New(authn.Principal{
		Account:    st.Account,
		Methods:    st.Methods,
		Assurance:  st.Assurance,
		IDPAssured: st.IDPAssured,
		AuthTime:   st.AuthTime,
		Provider:   st.Provider,
	}, "", "")
}

// sessionOwns accepts the flow's account, or during impersonation the operator's account and a row that names a record.
func sessionOwns(id authn.Identity, st authn.SessionStatus) bool {
	if id.Impersonating() {
		return st.Account == id.Actor() && st.ImpersonationID != ""
	}
	return st.Account == id.Account()
}

// idpAttributes drops a key outside the reserved idp. prefix and cuts the key and value to the session payload's bounds.
func idpAttributes(ext *authn.External) map[string]string {
	if ext == nil || len(ext.Attributes) == 0 {
		return nil
	}
	out := make(map[string]string, len(ext.Attributes))
	for key, value := range ext.Attributes {
		if !strings.HasPrefix(key, authn.ReservedPrefixIDP) {
			continue
		}
		if len(key) > authn.SessionIDPKeyMax {
			continue
		}
		out[key] = cutUTF8(value, authn.SessionIDPValueMax)
		if len(out) == authn.SessionIDPAttributesMax {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

const actionLogin = "authn.login"

const (
	auditOutcomeFailure   = "failure"
	auditOutcomeThrottled = "throttled"
	auditOutcomeRefused   = "refused"
)

func loginAuditOutcome(outcome string) (string, bool) {
	switch outcome {
	case outcomeFailed:
		return auditOutcomeFailure, true
	case outcomeThrottled:
		return auditOutcomeThrottled, true
	case outcomeRefused:
		return auditOutcomeRefused, true
	}
	return "", false
}

// auditLoginOutcome writes only the method and delivery as detail, never the input or a free-text reason.
func (r *routes) auditLoginOutcome(ctx context.Context, rec record, outcome string) {
	if r.audit == nil || rec.Purpose != authn.PurposeLogin {
		return
	}
	err := r.audit.Record(ctx, audit.Entry{
		Action:  actionLogin,
		Object:  audit.AccountObject(rec.Account),
		Outcome: outcome,
		Detail:  map[string]string{"method": rec.Method, "delivery": rec.Delivery},
	})
	if err != nil {
		r.meters.auditFailed(ctx)
		r.logger.Error(ModuleName+": a login audit row could not be written",
			slog.String("cause", err.Error()))
	}
}

func (r *routes) auditLogin(c *call, rec record, account authn.AccountID, session authn.SessionID, methods []authn.Method, result authn.MFAResult) {
	if r.audit == nil {
		return
	}
	base := c.ctx
	now := r.clock.Now()
	id := identity.New(authn.Principal{
		Account:    account,
		Session:    session,
		Methods:    methods,
		Assurance:  result.Assurance,
		IDPAssured: result.IDPAssured,
		AuthTime:   now,
	}, "", "")
	actx := identity.With(base, id)
	err := r.audit.Record(actx, audit.Entry{
		Action:  actionLogin,
		Object:  audit.AccountObject(string(account)),
		Outcome: "success",
		Detail:  map[string]string{"method": rec.Method, "delivery": rec.Delivery},
	})
	if err != nil {
		r.meters.auditFailed(base)
		log.FromContext(base).Error(ModuleName+": a login audit row could not be written",
			slog.String("cause", err.Error()))
	}
}

func (r *routes) publishLogin(ctx context.Context, account authn.AccountID, session authn.SessionID, methods []authn.Method, result authn.MFAResult, delivery string) {
	if r.bus == nil {
		return
	}
	names := make([]string, len(methods))
	for i, m := range methods {
		names[i] = string(m)
	}
	payload := authn.LoginSucceeded{
		AccountID: string(account),
		SessionID: string(session),
		Methods:   strings.Join(names, ","),
		Assurance: assuranceName(result.Assurance),
		Delivery:  delivery,
	}
	if err := r.bus.Publish(ctx, event.Event{Type: authn.EventLoginSucceeded, Payload: payload}); err != nil {
		log.FromContext(ctx).Warn(ModuleName+": an event could not be published",
			slog.String("event_type", authn.EventLoginSucceeded), slog.String("cause", err.Error()))
	}
}

const actionBindingLink = "authn.binding.link"

func (r *routes) flowIdentity(rec record) authn.Identity {
	p := authn.Principal{
		Account:  authn.AccountID(rec.Account),
		Session:  authn.SessionID(rec.Session),
		AuthTime: r.clock.Now(),
	}
	if rec.Impersonation != "" {
		p.Actor = authn.AccountID(rec.Actor)
		p.Scope = rec.Scope
	}
	return identity.New(p, "", "")
}

func (r *routes) flowAuditContext(ctx context.Context, rec record) context.Context {
	return identity.With(ctx, r.flowIdentity(rec))
}

func (r *routes) auditBindingLink(c *call, rec record, provider string) {
	if r.audit == nil {
		return
	}
	base := c.ctx
	account := authn.AccountID(rec.Account)
	detail := map[string]string{}
	if class, known := bindingProviderClass(provider); known {
		detail["provider"] = class
	}
	err := r.audit.Record(r.flowAuditContext(base, rec), audit.Entry{
		Action:  actionBindingLink,
		Object:  audit.AccountObject(string(account)),
		Outcome: outcomeSuccess,
		Detail:  detail,
	})
	if err != nil {
		log.FromContext(base).Error(ModuleName+": an audit row of a completed link could not be written",
			slog.String("cause", err.Error()))
	}
}

// publishBinding publishes authn.binding.changed only for a provider in the payload's closed class set.
func (r *routes) publishBinding(ctx context.Context, account authn.AccountID, provider string) {
	if r.bus == nil {
		return
	}
	class, known := bindingProviderClass(provider)
	if !known {
		log.FromContext(ctx).Warn(ModuleName + ": an external identity of a provider outside the classes of the " +
			"event payload was linked, so no authn.binding.changed was published")
		return
	}
	payload := authn.BindingChanged{
		AccountID:     string(account),
		ProviderClass: class,
		Change:        "linked",
		Source:        "explicit",
	}
	if err := r.bus.Publish(ctx, event.Event{Type: authn.EventBindingChanged, Payload: payload}); err != nil {
		log.FromContext(ctx).Warn(ModuleName+": an event could not be published",
			slog.String("event_type", authn.EventBindingChanged), slog.String("cause", err.Error()))
	}
}

func bindingProviderClass(provider string) (string, bool) {
	switch {
	case provider == "google", provider == "github", provider == "wechat":
		return provider, true
	case strings.HasPrefix(provider, "wechat-openid:"):
		return "wechat", true
	case strings.HasPrefix(provider, "oidc:"):
		return "oidc", true
	case strings.HasPrefix(provider, "saml:"):
		return "saml", true
	}
	return "", false
}

func assuranceName(a authn.Assurance) string {
	switch a {
	case authn.AssuranceMulti:
		return "multi"
	case authn.AssuranceSingle:
		return "single"
	default:
		return "none"
	}
}
