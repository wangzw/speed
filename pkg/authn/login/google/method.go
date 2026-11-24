package google

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/vislake/speed/pkg/authn"
)

const stepAuthorize = "authorize"

var scopes = []string{oidc.ScopeOpenID, "email", "profile"}

const paramHostedDomain = "hd"

const idTokenKey = "id_token"

var (
	errRefused = fmt.Errorf("%s: %w", ModuleName, authn.ErrInvalidCredentials)

	errProviderRefused = fmt.Errorf("%s: the provider reported the request was refused: %w", ModuleName, authn.ErrInvalidCredentials)

	errNotCallback = errors.New("authn.login.google: the answer is not a redirect back")
	errUnknownStep = errors.New("authn.login.google: the flow is at a step this method did not name")
	errBadState    = errors.New("authn.login.google: the flow carries no drawn state")
	errNoIDToken   = errors.New("authn.login.google: the token response carried no identity token")
)

// Name is the method's stable name.
func (p *product) Name() string { return methodName }

// Describe reports the Google method: a redirect button that speaks OIDC and
// opens an account for a Google identity no account holds yet.
func (p *product) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             methodName,
		Kind:             authn.MethodKindRedirect,
		Protocol:         authn.MethodProtocolOIDC,
		Label:            "Google",
		OpensAccount:     true,
		BindingProviders: []string{providerName},
	}
}

// Begin starts a flow: it draws the state, the PKCE verifier and the nonce, keeps them private, and answers the authorization URL.
func (p *product) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	state, err := randomToken()
	if err != nil {
		return unavailable("drawing the state", err), nil
	}
	nonce, err := randomToken()
	if err != nil {
		return unavailable("drawing the nonce", err), nil
	}
	verifier := oauth2.GenerateVerifier()

	provider, err := p.provider.get(ctx)
	if err != nil {
		return unavailable("discovering the provider", err), nil
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)}
	if p.config.hostedDomain != "" {
		opts = append(opts, oauth2.SetAuthURLParam(paramHostedDomain, p.config.hostedDomain))
	}
	to := p.oauthConfig(provider).AuthCodeURL(state, opts...)
	return authn.StepRedirect(stepAuthorize, writeState(digest(state), verifier, nonce), to), nil
}

// Answer handles the redirect back: a provider error is refused first, then the state is checked, then the code is exchanged and the token verified.
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
	if subtle.ConstantTimeCompare([]byte(digest(in.Query.Get("state"))), []byte(st.State)) != 1 {
		return authn.StepRefused(errRefused), nil
	}
	if iss := in.Query.Get("iss"); iss != "" && iss != p.provider.issuer {
		return authn.StepRefused(errRefused), nil
	}
	code := in.Query.Get("code")
	if code == "" {
		return authn.StepRefused(errRefused), nil
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err), nil
	}
	provider, err := p.provider.get(ctx)
	if err != nil {
		return unavailable("discovering the provider", err), nil
	}
	return p.exchange(ctx, provider, code, st)
}

func (p *product) exchange(ctx context.Context, provider *oidc.Provider, code string, st methodState) (authn.Step, error) {
	ctx = oidc.ClientContext(ctx, p.client)
	token, err := p.oauthConfig(provider).Exchange(ctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return exchangeAnswer(err), nil
	}
	rawIDToken, ok := token.Extra(idTokenKey).(string)
	if !ok || rawIDToken == "" {
		return unavailable("reading the identity token", errNoIDToken), nil
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: p.config.clientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		// go-oidc flattens a key-set fetch failure into the same error as a
		// signature failure, so a key set that could not be reached is refused
		// here too; the discovery and the token exchange are where a provider
		// outage is told apart.
		return authn.StepRefused(errRefused), nil
	}
	var claims idClaims
	if err := idToken.Claims(&claims); err != nil {
		return authn.StepRefused(errRefused), nil
	}
	if idToken.Subject == "" {
		return authn.StepRefused(errRefused), nil
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(st.Nonce)) != 1 {
		return authn.StepRefused(errRefused), nil
	}
	if len(idToken.Audience) > 1 && claims.AZP != p.config.clientID {
		return authn.StepRefused(errRefused), nil
	}
	if p.config.hostedDomain != "" && claims.HostedDomain != p.config.hostedDomain {
		return authn.StepRefused(errRefused), nil
	}
	return authn.StepProven(authn.Proof{
		External: &authn.External{
			Provider:      providerName,
			Subject:       idToken.Subject,
			Email:         claims.Email,
			EmailVerified: bool(claims.EmailVerified),
			DisplayName:   claims.Name,
		},
		Methods: []authn.Method{authn.MethodOAuth},
	}), nil
}

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

func exchangeAnswer(err error) authn.Step {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < http.StatusInternalServerError {
		return authn.StepRefused(errRefused)
	}
	return unavailable("exchanging the authorization code", err)
}

func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("%s: %s: %w", ModuleName, doing, err))
}

func (p *product) oauthConfig(provider *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.config.clientID,
		ClientSecret: p.config.clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  p.config.redirectURL,
		Scopes:       scopes,
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type idClaims struct {
	Nonce         string    `json:"nonce"`
	AZP           string    `json:"azp"`
	Email         string    `json:"email"`
	EmailVerified boolClaim `json:"email_verified"`
	Name          string    `json:"name"`
	HostedDomain  string    `json:"hd"`
}

type boolClaim bool

// UnmarshalJSON reads a boolean or the string form of one.
func (b *boolClaim) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true", `"true"`:
		*b = true
	case "false", `"false"`, "null":
		*b = false
	default:
		return errors.New("authn.login.google: a boolean claim is neither a boolean nor a string boolean")
	}
	return nil
}
