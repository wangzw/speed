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

// stepAuthorize is this method's name for its one step: the flow is at the
// redirect to the provider, waiting for the redirect back. The engine keeps the
// name and hands it back in FlowState.Step, so a callback that arrives at a step
// this method did not name is refused.
const stepAuthorize = "authorize"

// scopes are what the method asks the provider for: the OpenID scope for the
// identity token, email for the address and its verified flag, and profile for
// the display name.
var scopes = []string{oidc.ScopeOpenID, "email", "profile"}

// paramHostedDomain is the authorization request parameter that asks the
// provider to restrict the account chooser to a Workspace domain. It is a
// display optimization; the check is on the token's hd claim.
const paramHostedDomain = "hd"

// idTokenKey is the field of the token endpoint's response that carries the
// identity token.
const idTokenKey = "id_token"

// The causes the method puts on an answer. None carries a value the client sent,
// and none names an authorization code, a token, a state or a nonce.
var (
	// errRefused is the cause of every refusal: a state that does not match, an
	// issuer that does not match, a token that does not verify, a nonce that does
	// not match, an authorized party that is not this client, a hosted domain that
	// does not match. They read the same here, in the log as on the wire.
	errRefused = fmt.Errorf("%s: %w", ModuleName, authn.ErrInvalidCredentials)

	// errProviderRefused is the cause when the provider itself reported that the
	// request was refused, which the common case of the user declining is. It is
	// a refusal and not an outage.
	errProviderRefused = fmt.Errorf("%s: the provider reported the request was refused: %w", ModuleName, authn.ErrInvalidCredentials)

	errNotCallback = errors.New("authn.login.google: the answer is not a redirect back")
	errUnknownStep = errors.New("authn.login.google: the flow is at a step this method did not name")
	errBadState    = errors.New("authn.login.google: the flow carries no drawn state")
	errNoIDToken   = errors.New("authn.login.google: the token response carried no identity token")
)

// Name is the method's stable name.
func (p *product) Name() string { return methodName }

// Begin starts a flow: it draws the state, the PKCE verifier and the nonce,
// keeps them in the flow's private state, and answers the authorization URL the
// user agent leaves for. A provider that cannot be discovered is an outage, not
// a refusal.
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

// Answer handles the redirect back from the provider. The engine has already
// checked the flow's state, the method's name and the path, and moved the record
// to processing, so the authorization code is exchanged at most once. The order
// is the design's: an error parameter first, then the state, then the issuer,
// then the exchange and the token checks.
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
	// A provider that reported an error answered nothing usable, and the request
	// is refused before any other parameter, the state included, is read.
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

// exchange takes the authorization code to the token endpoint and turns the
// identity token into a proof. A token endpoint that answered with a refusal is
// a refusal; a transport failure or a server error is an outage. A token that
// does not verify, a nonce that does not match and a hosted domain that does not
// match are all refusals.
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
		// A token that does not verify is a refusal. The library flattens the
		// error of its own key-set fetch into the same string as a signature
		// failure, so a key set that could not be reached is answered here as a
		// refusal too; the discovery and the token exchange, which are reached
		// with the call's own context, are where a provider outage is told apart.
		return authn.StepRefused(errRefused), nil
	}
	var claims idClaims
	if err := idToken.Claims(&claims); err != nil {
		return authn.StepRefused(errRefused), nil
	}
	if idToken.Subject == "" {
		return authn.StepRefused(errRefused), nil
	}
	// The library verifies the signature, the issuer, the audience and the
	// expiry; it does not verify the nonce, and it does not check the authorized
	// party when the token carries more than one audience. Both are checked here.
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

// servesPurpose reports whether the method answers a flow of this purpose. A
// Google identity proves which external identity a caller holds: a login uses
// it to resolve an account, a link binds it to the flow's account, and a step-up
// re-proves an account whose first factor is this one. An enrollment registers a
// factor, which is not something an external identity proves. The zero purpose
// is none.
func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

// refusePurpose refuses a flow of a purpose the method does not serve, before it
// draws anything.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}

// exchangeAnswer maps a failed exchange to an answer. A token endpoint that
// answered with a client error refused the code; a transport failure, a context
// that ended and a server error are all an outage.
func exchangeAnswer(err error) authn.Step {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < http.StatusInternalServerError {
		return authn.StepRefused(errRefused)
	}
	return unavailable("exchanging the authorization code", err)
}

// unavailable answers that the method cannot say now. The dependency's error
// stays in the chain, so that the engine finds an ErrUnavailable in it, and the
// text names the step and nothing the client sent.
func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("%s: %s: %w", ModuleName, doing, err))
}

// oauthConfig is the OAuth client for the discovered provider: the client's own
// credentials, the provider's endpoints and this provider's redirect URI.
func (p *product) oauthConfig(provider *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.config.clientID,
		ClientSecret: p.config.clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  p.config.redirectURL,
		Scopes:       scopes,
	}
}

// randomToken is 32 bytes of crypto/rand as unpadded base64url, for the state
// and the nonce. Its length and encoding keep it a single URL-safe parameter.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// idClaims are the identity token's claims this method reads besides what the
// library parses. The subject, the audience, the expiry and the issuer come from
// the parsed token, not from here.
type idClaims struct {
	Nonce         string    `json:"nonce"`
	AZP           string    `json:"azp"`
	Email         string    `json:"email"`
	EmailVerified boolClaim `json:"email_verified"`
	Name          string    `json:"name"`
	HostedDomain  string    `json:"hd"`
}

// boolClaim is a claim that is a JSON boolean but that a provider may send as
// the string "true" or "false". Anything else, a number included, is an error,
// which refuses the token.
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
