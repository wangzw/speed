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

const stepAuthorize = "authorize"

const fieldProvider = "provider"

const idTokenKey = "id_token"

const actionReject = "authn.sso.reject"

const namespaceOIDC = "oidc:"

var (
	errRefused = fmt.Errorf("%s: %w", ModuleName, authn.ErrInvalidCredentials)

	errProviderRefused = fmt.Errorf("%s: the provider reported the request was refused: %w", ModuleName, authn.ErrInvalidCredentials)

	errNotCallback = errors.New("authn.sso.oidc: the answer is not a redirect back")
	errUnknownStep = errors.New("authn.sso.oidc: the flow is at a step this method did not name")
	errBadState    = errors.New("authn.sso.oidc: the flow carries no drawn state")
	errNoProvider  = errors.New("authn.sso.oidc: the start input names no provider")
	errNoIDToken   = errors.New("authn.sso.oidc: the token response carried no identity token")
)

func (p *product) Name() string { return methodName }

// Describe reports the enterprise OIDC method as an entry that discovers the provider and leaves for it, opening no account of its own.
func (p *product) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             methodName,
		Kind:             authn.MethodKindEnterprise,
		Protocol:         authn.MethodProtocolOIDC,
		BindingProviders: []string{namespaceOIDC},
	}
}

// Begin reads the named provider, keeps the state, PKCE verifier and nonce in the flow's private state, and answers the authorization URL. A provider that is absent, disabled, of another protocol or undiscoverable is an outage.
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

// Answer handles the redirect: it checks the state, exchanges the authorization code and verifies the identity token. The engine has already bound the flow, so the code is exchanged at most once.
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
	if in.Query.Get("error") != "" {
		return authn.StepRefused(errProviderRefused), nil
	}
	st, ok := readState(f.Private)
	if !ok {
		return authn.StepRefused(errBadState), nil
	}
	id := st.Provider
	// The state is compared with the flow's digest in constant time, so a callback for another flow is refused.
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
	// The redirect's iss, when sent, must equal the configured issuer verbatim.
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
		// The library flattens a bad signature, foreign audience, foreign issuer, expiry and key-set failure into one error, so an unreachable key set is refused here too.
		return p.reject(ctx, id, classifyVerify(rawIDToken, cfg, time.Now()), errRefused)
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	sum := buildProfile(cfg, idToken.Subject, claims)
	// A subject has to be present and the mapped subject non-empty, so an identity never binds on an empty key.
	if idToken.Subject == "" || sum.subject == "" {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	// The library verifies the signature, issuer, audience and expiry but not the nonce or the authorized party with several audiences; both are checked here.
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(st.Nonce)) != 1 {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	if len(idToken.Audience) > 1 && stringClaim(claims["azp"]) != cfg.ClientID {
		return p.reject(ctx, id, outcomeAudience, errRefused)
	}

	p.meters.assertion(ctx, protocolOIDC, outcomeAccepted)
	return authn.StepProven(authn.Proof{
		External: &authn.External{
			Provider:          namespaceOIDC + id,
			Subject:           sum.subject,
			Email:             sum.email,
			EmailVerified:     sum.emailVerified,
			DisplayName:       sum.displayName,
			PreferredUsername: sum.preferredUsername,
			Tenant:            prov.Tenant,
			Attributes:        sum.attributes,
		},
		Methods:     []authn.Method{authn.MethodOIDC},
		IDPAsserted: idpAsserted(prov, cfg, claims),
		Provider:    id,
	})
}

func idpAsserted(prov authn.SSOProvider, cfg providerConfig, claims map[string]any) bool {
	if !prov.TrustIDPMFA {
		return false
	}
	acr := stringClaim(claims["acr"])
	return acr != "" && slices.Contains(cfg.MFAACRValues, acr)
}

func (p *product) exchangeAnswer(ctx context.Context, id string, err error) authn.Step {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < http.StatusInternalServerError {
		return p.reject(ctx, id, outcomeOther, errRefused)
	}
	return p.outage(ctx, id, "exchanging the authorization code", err)
}

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

func (p *product) outage(ctx context.Context, idpID, doing string, cause error) authn.Step {
	attrs := []slog.Attr{slog.String("method", methodName), slog.String("cause", cause.Error())}
	if idpID != "" {
		attrs = append(attrs, slog.String("idp", idpID))
	}
	p.logger.LogAttrs(ctx, slog.LevelWarn, ModuleName+": "+doing+" failed", attrs...)
	return authn.StepUnavailable(fmt.Errorf("%s: %s: %w", ModuleName, doing, cause))
}

var errProviderNotUsable = errors.New("the provider is not a usable OIDC provider")

func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}
