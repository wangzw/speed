// Package github carries the GitHub login method: the first factor that proves
// an external identity by an OAuth 2.0 authorization-code flow.
//
// GitHub is not an OpenID provider and issues no ID token, so the method reads
// the identity from the access token it exchanged the code for: the numeric
// account number from /user is the subject, and an address from /user/emails is
// taken only when one entry is both primary and verified. The address /user
// carries has no verified marker and is never used. The access token is used
// once and discarded: the method stores it nowhere, refreshes nothing and hands
// it to no one.
//
// The method holds no flow state of its own beyond the opaque bytes the engine
// keeps for it: the anti-forgery state and the PKCE verifier travel in the flow
// record's method state and are checked exactly once, on the callback. It
// touches no HTTP server, session, token or flow record, and it writes no log
// line: the engine logs the cause of an answer, and no cause names the code,
// the token, the state or the verifier.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine and a gateway. It delivers the login method capability
// non-exclusively, so it stands beside the other methods. Its outbound requests
// go through the restricted client of internal/egress.
package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oauth2"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login.github"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// gateNamespace is where the keys this module reads from the gateway's section
// are mounted. The gateway declares them; a module that builds an address from
// the public base reads them back the same way.
const gateNamespace = "authn.gate"

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, in flow records and in metric attributes, and a published
// name never changes. The callback path's provider segment is this name.
const methodName = "github"

// The names and fixed values of the flow's one step and of the provider's
// endpoints. The authorization-code flow is one step: the method sends the user
// agent to the provider, and the provider sends it back to the callback route.
const (
	stepRedirect = "redirect"

	// callbackPath is the route the provider sends the browser back to,
	// relative to the capability's base path, ending in this method's name.
	callbackPath = "/callback/" + methodName

	userPath   = "/user"
	emailsPath = "/user/emails"

	// githubAPIBase is the base the two reads are resolved against. It is the
	// API host, which is not the host the authorization and token endpoints
	// live on.
	githubAPIBase    = "https://api.github.com"
	githubAPIVersion = "2022-11-28"
	userAgent        = "speed-authn"
)

// scopes are the permissions the method asks for: the account profile and the
// list of addresses, which the primary-and-verified read needs.
var scopes = []string{"read:user", "user:email"}

// The callback parameters the method reads.
const (
	paramError = "error"
	paramState = "state"
	paramCode  = "code"
)

// stateBytes is the length of the anti-forgery state's random part.
const stateBytes = 32

// stateVersion is the version of the method state between the step that
// redirects and the callback that answers it.
const stateVersion = 1

// method is this module's product.
type method struct {
	clientID     string
	clientSecret string
	redirectURI  string
	endpoint     oauth2.Endpoint
	apiBase      string
	client       *http.Client
}

// method must satisfy the capability it delivers.
var _ authn.LoginMethod = (*method)(nil)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The module resolves the configuration reader itself, because the OAuth
// application's credentials and the public base the callback address is built
// from are read there; a command with no reader fails in resolution. The login
// method capability is delivered without being claimed, which is what lets
// several methods stand side by side. The logging capability is taken up as an
// optional requirement, because the client secret is registered as a literal
// redaction rule and an assembly with no logging has nothing to register it
// with.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			if err := registerSecret(reg, cfg.clientSecret); err != nil {
				return nil, err
			}
			return newMethod(cfg, egress.New(egress.Options{})), nil
		},
	}
}

// readerOf takes up the configuration reader. Its absence means the host never
// handed one to the registry before the drive began, which is core's own
// failure and is carried out as it stands: reading it as "nothing was
// configured" would lose the credentials in silence.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the OAuth application's credentials and the public base are read "+
			"from configuration, and no reader was handed to the registry before the assembly began: %w",
			ModuleName, err)
	}
	return reader, nil
}

// registerSecret registers the client secret as a literal redaction rule, so it
// is masked wherever it turns up in a log line. An assembly with no logging has
// nothing to register it with and registers nothing; a registration after a
// logger has bound an attribute would not reach what was bound, so this runs in
// New, before any logger this module would take.
func registerSecret(reg *core.Registry, secret string) error {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register the client secret "+
			"with: %w", ModuleName, err)
	}
	literal.Register(delivered.Redaction(), ModuleName+".client-secret", secret)
	return nil
}

// newMethod builds the product over the resolved configuration and the
// outbound client. Production passes the restricted client; a case passes the
// same client pointed at a loopback server.
func newMethod(cfg resolved, client *http.Client) *method {
	return &method{
		clientID:     cfg.clientID,
		clientSecret: cfg.clientSecret,
		redirectURI:  cfg.redirectURI,
		endpoint:     cfg.endpoint,
		apiBase:      cfg.apiBase,
		client:       client,
	}
}

// Name is the method's stable name.
func (m *method) Name() string { return methodName }

// oauthConfig is the OAuth 2.0 configuration one exchange is run with.
func (m *method) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     m.clientID,
		ClientSecret: m.clientSecret,
		RedirectURL:  m.redirectURI,
		Endpoint:     m.endpoint,
		Scopes:       scopes,
	}
}

// Begin starts a flow: it generates the anti-forgery state and the PKCE verifier,
// keeps them in the method state, and answers with the provider's authorization
// address. The state is the code's own, never the request's, and the challenge
// is S256.
func (m *method) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err), nil
	}
	st, state, err := newState()
	if err != nil {
		return unavailable("starting the flow", err), nil
	}
	to := m.oauthConfig().AuthCodeURL(state, oauth2.S256ChallengeOption(st.Verifier))
	return authn.StepRedirect(stepRedirect, st.private(), to), nil
}

// Answer handles the provider's redirect back: an error parameter ends the flow
// as refused, the state is compared to the recorded digest, and the code is
// exchanged for an access token the method reads the identity with and then
// drops. A callback that is not a redirect, a purpose the method does not serve,
// and a step the method never named are refused, so a flow record this method
// did not write cannot be walked into a proof.
func (m *method) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputCallback {
		return authn.StepRefused(errNotCallback), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	if f.Step != stepRedirect {
		return authn.StepRefused(errUnknownStep), nil
	}
	// The provider's own error ends the flow here, before any other parameter is
	// read: a refusal to authorize is not a callback that can be run.
	if first(in, paramError) != "" {
		return authn.StepRefused(errCancelled), nil
	}
	st, ok := readState(f.Private)
	if !ok {
		return authn.StepRefused(errBadState), nil
	}
	if !stateMatches(st.State, first(in, paramState)) {
		return authn.StepRefused(errStateMismatch), nil
	}
	code := first(in, paramCode)
	if code == "" {
		return authn.StepRefused(errNoCode), nil
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err), nil
	}
	return m.identify(ctx, code, st.Verifier), nil
}

// identify exchanges the authorization code and reads the identity the access
// token names. The access token is carried in the request's header and is not
// stored; the token and the code reach no answer, no error text and no log.
func (m *method) identify(ctx context.Context, code, verifier string) authn.Step {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, m.client)
	tok, err := m.oauthConfig().Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		// The provider answered that it refuses the exchange. Any other failure
		// is the provider or the network being unreachable.
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) {
			return authn.StepRefused(errRefused)
		}
		return unavailable("exchanging the authorization code", err)
	}
	user, err := m.fetchUser(ctx, tok.AccessToken)
	if err != nil {
		return readFailure("reading the account", err)
	}
	if user.ID <= 0 {
		return authn.StepRefused(errNoSubject)
	}
	email, verified, err := m.fetchPrimaryVerifiedEmail(ctx, tok.AccessToken)
	if err != nil {
		return readFailure("reading the addresses", err)
	}
	return authn.StepProven(authn.Proof{
		External: &authn.External{
			Provider:      methodName,
			Subject:       strconv.FormatInt(user.ID, 10),
			Email:         email,
			EmailVerified: verified,
			DisplayName:   displayName(user),
		},
		Methods: []authn.Method{authn.MethodOAuth},
	})
}

// servesPurpose reports whether the method answers a flow of this purpose. An
// external identity is what a login proves, what a link binds and what a step-up
// on an account with no factor proves; an enrollment registers a factor and a
// provider's identity is not one. The zero purpose is none.
func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	}
	return false
}

// refusePurpose refuses a flow of a purpose the method does not serve, before it
// generates or reads anything.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("authn.login.github: purpose %q is not served", purpose))
}

// displayName is the name shown for the identity: the profile name the provider
// reports, or the login when it reports none. It is a display value and no part
// of any judgement, and the resolver trims and bounds it before an account
// carries it.
func displayName(u githubUser) string {
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	return strings.TrimSpace(u.Login)
}

// first reads a callback parameter, from the query of a redirect or from the
// form of a POST binding. An absent parameter is the empty string.
func first(in authn.StepInput, key string) string {
	if v := in.Query.Get(key); v != "" {
		return v
	}
	return in.Form.Get(key)
}

// The causes the method puts on an answer. None carries the code, the token, the
// state or the verifier.
var (
	// errRefused is the cause of every refusal of a callback: an exchange the
	// provider refused and a read the provider answered with an error status all
	// read the same here, in the log as on the wire.
	errRefused = fmt.Errorf("authn.login.github: %w", authn.ErrInvalidCredentials)

	errNotCallback   = errors.New("authn.login.github: the answer is not a callback")
	errUnknownStep   = errors.New("authn.login.github: the flow is at a step this method did not name")
	errCancelled     = errors.New("authn.login.github: the provider reported the request was not authorized")
	errBadState      = errors.New("authn.login.github: the flow carries no state to compare")
	errStateMismatch = errors.New("authn.login.github: the state does not match the flow")
	errNoCode        = errors.New("authn.login.github: the callback carries no authorization code")
	errNoSubject     = errors.New("authn.login.github: the provider reported no account number")
)

// unavailable answers that the method cannot say now. The dependency's error
// stays in the chain, so that the engine finds an ErrUnavailable in it, and the
// text names the step and nothing the client sent.
func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.login.github: %s: %w", doing, err))
}

// readFailure maps a failed read of the provider's API to an answer: a status
// that is not 200 is the provider refusing the request, which is a refusal, and
// any other failure is the provider being unable to answer, which is an outage.
func readFailure(doing string, err error) authn.Step {
	var pe *providerError
	if errors.As(err, &pe) {
		return authn.StepRefused(errRefused)
	}
	return unavailable(doing, err)
}

// flowState is the method state between the step that redirects and the callback
// that answers it: the digest of the anti-forgery state and the PKCE verifier.
// It holds the state as a digest and no other secret.
type flowState struct {
	Version  int    `json:"v"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
}

// private renders the method state the engine keeps as it stands.
func (s flowState) private() []byte {
	// A struct of an int and two strings always encodes.
	b, _ := json.Marshal(s)
	return b
}

// newState generates the anti-forgery state and the PKCE verifier. The state is
// 32 random bytes in base64url; the verifier is the OAuth library's, which is 32
// random bytes in base64url too. The state is returned as it was generated and
// kept as its SHA-256 digest.
func newState() (flowState, string, error) {
	var raw [stateBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return flowState{}, "", err
	}
	state := base64.RawURLEncoding.EncodeToString(raw[:])
	verifier := oauth2.GenerateVerifier()
	sum := sha256.Sum256([]byte(state))
	return flowState{Version: stateVersion, State: hex.EncodeToString(sum[:]), Verifier: verifier}, state, nil
}

// readState reads the state newState wrote. Anything else, empty state
// included, names no state to compare.
func readState(private []byte) (flowState, bool) {
	var s flowState
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion || s.State == "" || s.Verifier == "" || dec.More() {
		return flowState{}, false
	}
	return s, true
}

// stateMatches reports whether the state a callback carried hashes to the
// recorded digest, compared in constant time. A malformed digest matches
// nothing.
func stateMatches(digestHex, state string) bool {
	want, err := hex.DecodeString(digestHex)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(state))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}
