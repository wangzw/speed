package login

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
)

// statusIdentity builds the identity a verifying backend would build from a
// session's status, for reading the freshness a completion review requires. It
// stamps no backend and no challenge: it is used for a judgement, not for a
// request.
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

// idpAttributes is the idp.* snapshot an external identity's attribute mapping
// produced, held to the bound the session payload carries: at most the declared
// number of entries, a key within the declared length including the prefix, and
// a value cut at a character boundary to the declared length. A key outside the
// reserved prefix is dropped, since only the mapping's own attributes belong in
// the snapshot.
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

// cutUTF8 shortens s to at most n bytes without splitting a character, and
// returns s itself when it is already within the bound.
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

// The audit action of a login, and the event payloads' field sources.
const actionLogin = "authn.login"

// auditLogin writes the login audit row. The row's actor is the account the
// login just proved, which is carried by a context derived for this one call:
// identity.With puts an identity into it, and the context reaches the audit
// record alone. It is never handed to a handler and never becomes the request's
// own context, so it does not flow back into the request chain. The row is
// best-effort: a failure is an Error and never fails the login.
func (r *routes) auditLogin(c *gin.Context, rec record, account authn.AccountID, session authn.SessionID, methods []authn.Method, result authn.MFAResult) {
	if r.audit == nil {
		return
	}
	base := c.Request.Context()
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
		Object:  string(account),
		Outcome: "success",
		Detail:  map[string]string{"method": rec.Method, "delivery": rec.Delivery},
	})
	if err != nil {
		log.FromContext(base).Error(ModuleName+": a login audit row could not be written",
			slog.String("cause", err.Error()))
	}
}

// publishLogin publishes authn.login.succeeded. A failure is a Warn and never
// blocks the login: an event is a hint.
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

// publishBinding publishes authn.binding.changed for a completed link. The
// provider class is the closed set the payload carries; a provider written in
// none of the classes publishes no event rather than one with a value outside
// the set, exactly as the unlink path does, and the caller's audit row still
// records the link.
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

// bindingProviderClass is the class of a provider in the closed set the
// authn.binding.changed payload carries: google, github, wechat, oidc or saml.
// A provider written in none of the namespaces has no class.
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

// assuranceName is the level's name, never its number, as the event payload
// carries it.
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
