package oidc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

// stepAuthorize is this method's name for its one step: the flow is at the
// redirect to the provider, waiting for the redirect back. The engine keeps the
// name and hands it back in FlowState.Step, so a callback that arrives at a step
// this method did not name is refused.
const stepAuthorize = "authorize"

// fieldProvider is the start input key that names the identity provider. It is
// the identifier the callback path and the flow record carry.
const fieldProvider = "provider"

// idTokenKey is the field of the token endpoint's response that carries the
// identity token.
const idTokenKey = "id_token"

// actionReject is the audit action of an identity token the method refused.
const actionReject = "authn.sso.reject"

// namespaceOIDC is the namespace an enterprise OIDC identity's subject is keyed
// in: the provider identifier follows the colon.
const namespaceOIDC = "oidc:"

// The causes the method puts on an answer. None carries a value the client sent,
// and none names an authorization code, a token, a state or a nonce.
var (
	// errRefused is the cause of every refusal of an identity token: a state that
	// does not match, an issuer that does not match, a token that does not
	// verify, a nonce that does not match or an authorized party that is not this
	// client. They read the same here, in the log as on the wire.
	errRefused = fmt.Errorf("%s: %w", ModuleName, authn.ErrInvalidCredentials)

	// errProviderRefused is the cause when the provider reported that the request
	// was refused, which the common case of the user declining is. It is a
	// refusal and not an outage, and it is not an assertion: no outcome is
	// recorded for it.
	errProviderRefused = fmt.Errorf("%s: the provider reported the request was refused: %w", ModuleName, authn.ErrInvalidCredentials)

	errNotCallback = errors.New("authn.sso.oidc: the answer is not a redirect back")
	errUnknownStep = errors.New("authn.sso.oidc: the flow is at a step this method did not name")
	errBadState    = errors.New("authn.sso.oidc: the flow carries no drawn state")
	errNoProvider  = errors.New("authn.sso.oidc: the start input names no provider")
	errNoIDToken   = errors.New("authn.sso.oidc: the token response carried no identity token")
)

// Name is the method's stable name.
func (p *product) Name() string { return methodName }

// Begin starts a flow: it reads the provider the start input names, draws the
// state, the PKCE verifier and the nonce, keeps them in the flow's private state
// and answers the authorization URL the user agent leaves for. A provider that
// is absent, disabled or of another protocol, and one that cannot be discovered,
// are an outage and not a refusal.
func (p *product) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	id := s.Fields[fieldProvider]
	if id == "" {
		return authn.StepRefused(errNoProvider), nil
	}
	prov, err := p.sso.Get(ctx, id)
	if err != nil {
		return p.outage(ctx, id, "reading the provider", err), nil
	}
	if !prov.Enabled || prov.Protocol != authn.SSOProtocolOIDC {
		return p.outage(ctx, id, "using the provider", errProviderNotUsable), nil
	}
	cfg, err := decodeProviderConfig(prov.Config)
	if err != nil {
		return p.outage(ctx, id, "reading the provider's configuration", err), nil
	}
	secret, err := p.sso.Secret(ctx, id)
	if err != nil {
		return p.outage(ctx, id, "reading the provider's client secret", err), nil
	}
	state, err := randomToken()
	if err != nil {
		return p.outage(ctx, id, "drawing the state", err), nil
	}
	nonce, err := randomToken()
	if err != nil {
		return p.outage(ctx, id, "drawing the nonce", err), nil
	}
	verifier := oauth2.GenerateVerifier()

	dctx, cancel := context.WithTimeout(ctx, p.settings.egressTimeout())
	defer cancel()
	_, oauthCfg, err := p.cache.get(dctx, id, prov.Version, cfg, secret, p.redirect.callback(id))
	if err != nil {
		return p.outage(ctx, id, "discovering the provider", err), nil
	}
	to := oauthCfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	return authn.StepRedirect(stepAuthorize, writeState(id, digest(state), verifier, nonce), to), nil
}

// Answer handles the redirect back from the provider. The engine has already
// checked the flow's binding, the method and the provider, and moved the record
// to processing, so the authorization code is exchanged at most once. The order
// is the design's: an error parameter first, then the state, then the issuer,
// then the exchange and the identity token checks.
func (p *product) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputCallback {
		return authn.StepRefused(errNotCallback), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	if f.Step != stepAuthorize {
		return authn.StepRefused(errUnknownStep), nil
	}
	// A provider that reported an error answered no assertion; the request is
	// refused before any other parameter is read.
	if in.Query.Get("error") != "" {
		return authn.StepRefused(errProviderRefused), nil
	}
	st, ok := readState(f.Private)
	if !ok {
		return authn.StepRefused(errBadState), nil
	}
	id := st.Provider
	// The digest of the returned state is compared with the one the flow drew, in
	// constant time, so a callback for another flow is refused.
	if subtle.ConstantTimeCompare([]byte(digest(in.Query.Get("state"))), []byte(st.State)) != 1 {
		return p.reject(ctx, id, outcomeOther, errRefused), nil
	}
	prov, err := p.sso.Get(ctx, id)
	if err != nil {
		return p.outage(ctx, id, "reading the provider", err), nil
	}
	if !prov.Enabled || prov.Protocol != authn.SSOProtocolOIDC {
		return p.outage(ctx, id, "using the provider", errProviderNotUsable), nil
	}
	cfg, err := decodeProviderConfig(prov.Config)
	if err != nil {
		return p.outage(ctx, id, "reading the provider's configuration", err), nil
	}
	// The issuer on the redirect, when the provider sends one, has to be the
	// configured issuer verbatim (RFC 9207).
	if iss := in.Query.Get("iss"); iss != "" && iss != cfg.Issuer {
		return p.reject(ctx, id, outcomeIssuer, errRefused), nil
	}
	code := in.Query.Get("code")
	if code == "" {
		return p.reject(ctx, id, outcomeOther, errRefused), nil
	}
	secret, err := p.sso.Secret(ctx, id)
	if err != nil {
		return p.outage(ctx, id, "reading the provider's client secret", err), nil
	}
	dctx, cancel := context.WithTimeout(ctx, p.settings.egressTimeout())
	defer cancel()
	provider, oauthCfg, err := p.cache.get(dctx, id, prov.Version, cfg, secret, p.redirect.callback(id))
	if err != nil {
		return p.outage(ctx, id, "discovering the provider", err), nil
	}
	return p.exchange(ctx, id, prov, cfg, provider, oauthCfg, code, st), nil
}

// exchange takes the authorization code to the token endpoint and turns the
// identity token into a proof. A token endpoint that answered with a refusal is
// a refusal; a transport failure or a server error is an outage. A token that
// does not verify, a nonce that does not match, an authorized party that is not
// this client and an empty subject are all refusals.
func (p *product) exchange(ctx context.Context, id string, prov authn.SSOProvider, cfg providerConfig, provider *oidc.Provider, oauthCfg *oauth2.Config, code string, st methodState) authn.Step {
	ectx, cancel := context.WithTimeout(ctx, p.settings.egressTimeout())
	defer cancel()
	token, err := oauthCfg.Exchange(oidc.ClientContext(ectx, p.client), code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return p.exchangeAnswer(ctx, id, err)
	}
	rawIDToken, ok := token.Extra(idTokenKey).(string)
	if !ok || rawIDToken == "" {
		return p.outage(ctx, id, "reading the identity token", errNoIDToken)
	}

	vctx, cancel2 := context.WithTimeout(ctx, p.settings.egressTimeout())
	defer cancel2()
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID, SupportedSigningAlgs: algsOf(cfg)})
	idToken, err := verifier.Verify(oidc.ClientContext(vctx, p.client), rawIDToken)
	if err != nil {
		// The library flattens a bad signature, a foreign audience, a foreign
		// issuer, an expiry and its own key-set fetch into one error, so a key set
		// that could not be reached is answered here as a refusal too; the
		// discovery and the token exchange, reached with the call's own context,
		// are where a provider outage is told apart.
		return p.reject(ctx, id, classifyVerify(rawIDToken, cfg, time.Now()), errRefused)
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	sum := buildProfile(cfg, idToken.Subject, claims)
	// The standard subject has to be there, and the subject the mapping produced
	// has to be one: an identity that binds on neither is refused rather than
	// bound on an empty key.
	if idToken.Subject == "" || sum.subject == "" {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	// The library verifies the signature, the issuer, the audience and the
	// expiry; it does not verify the nonce, and it does not check the authorized
	// party when the token carries more than one audience. Both are checked here.
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(st.Nonce)) != 1 {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	if len(idToken.Audience) > 1 && stringClaim(claims["azp"]) != cfg.ClientID {
		return p.reject(ctx, id, outcomeAudience, errRefused)
	}

	p.meters.assertion(ctx, protocolOIDC, outcomeAccepted)
	return authn.StepProven(authn.Proof{
		External: &authn.External{
			Provider:      namespaceOIDC + id,
			Subject:       sum.subject,
			Email:         sum.email,
			EmailVerified: sum.emailVerified,
			DisplayName:   sum.displayName,
			Tenant:        prov.Tenant,
			Attributes:    sum.attributes,
		},
		Methods:     []authn.Method{authn.MethodOIDC},
		IDPAsserted: idpAsserted(prov, cfg, claims),
		Provider:    id,
	})
}

// idpAsserted reports whether the identity provider declared a multi-factor
// authentication this deployment trusts: the provider row trusts the IdP's
// declaration and the token's acr is one of the values the provider configured.
// The amr claim takes no part.
func idpAsserted(prov authn.SSOProvider, cfg providerConfig, claims map[string]any) bool {
	if !prov.TrustIDPMFA {
		return false
	}
	acr := stringClaim(claims["acr"])
	return acr != "" && slices.Contains(cfg.MFAACRValues, acr)
}

// exchangeAnswer maps a failed exchange to an answer. A token endpoint that
// answered with a client error refused the code; a transport failure, a context
// that ended and a server error are all an outage.
func (p *product) exchangeAnswer(ctx context.Context, id string, err error) authn.Step {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < http.StatusInternalServerError {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	return p.outage(ctx, id, "exchanging the authorization code", err)
}

// reject answers a refusal of an identity token's judgement: it records the
// outcome on the authn.sso.assertions counter, writes a best-effort authn.sso.reject
// audit row naming the provider, logs the cause and answers StepRefused. No
// outcome, in the log as on the wire, carries a value the client sent.
func (p *product) reject(ctx context.Context, idpID, outcome string, cause error) authn.Step {
	p.meters.assertion(ctx, protocolOIDC, outcome)
	if idpID != "" && p.audit != nil {
		err := p.audit.Record(ctx, audit.Entry{Action: actionReject, Object: idpID, Outcome: outcome})
		if err != nil {
			p.logger.Warn(ModuleName+": an SSO reject audit row could not be written", slog.String("cause", err.Error()))
		}
	}
	p.logRefusal(ctx, idpID, outcome, cause)
	return authn.StepRefused(cause)
}

// logRefusal writes the one line of a refusal. A signature, an audience, an
// issuer or a time failure is what an operator acts on and reads as a Warn;
// every other refusal is the caller's and reads as Info.
func (p *product) logRefusal(ctx context.Context, idpID, outcome string, cause error) {
	level := slog.LevelInfo
	switch outcome {
	case outcomeSignature, outcomeAudience, outcomeIssuer, outcomeTime:
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{slog.String("method", methodName), slog.String("outcome", outcome)}
	if idpID != "" {
		attrs = append(attrs, slog.String("idp", idpID))
	}
	if cause != nil {
		attrs = append(attrs, slog.String("cause", cause.Error()))
	}
	p.logger.LogAttrs(ctx, level, ModuleName+": an enterprise identity was refused", attrs...)
}

// outage answers that the method cannot say now, because a provider row, its
// configuration, its secret or the provider itself could not be read. The log
// line names the step and the provider; the cause stays out of the answer, so
// that an outage of one provider reaches no other and tells the client nothing.
func (p *product) outage(ctx context.Context, idpID, doing string, cause error) authn.Step {
	attrs := []slog.Attr{slog.String("method", methodName), slog.String("cause", cause.Error())}
	if idpID != "" {
		attrs = append(attrs, slog.String("idp", idpID))
	}
	p.logger.LogAttrs(ctx, slog.LevelWarn, ModuleName+": "+doing+" failed", attrs...)
	return authn.StepUnavailable(fmt.Errorf("%s: %s: %w", ModuleName, doing, cause))
}

// errProviderNotUsable is the cause of an outage for a provider that is absent,
// disabled or of another protocol. Its text names no cause the client could
// distinguish.
var errProviderNotUsable = errors.New("the provider is not a usable OIDC provider")

// servesPurpose reports whether the method answers a flow of this purpose. An
// enterprise identity proves which external identity a caller holds: a login
// resolves an account from it, a link binds it to the flow's account, and a
// step-up re-proves an account whose first factor is this one. An enrollment
// registers a factor, which an external identity does not prove. The zero
// purpose is none.
func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

// refusePurpose refuses a flow of a purpose the method does not serve, before it
// reads anything.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}
