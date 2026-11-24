// Package github carries the GitHub login method, which proves an external identity by an OAuth 2.0 authorization-code flow.
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
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login.github"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const gateNamespace = "authn.gate"

const methodName = "github"

const (
	stepRedirect = "redirect"

	callbackPath = "/callback/" + methodName

	userPath   = "/user"
	emailsPath = "/user/emails"

	githubAPIBase    = "https://api.github.com"
	githubAPIVersion = "2022-11-28"
	userAgent        = "speed-authn"
)

var scopes = []string{"read:user", "user:email"}

const (
	paramError = "error"
	paramState = "state"
	paramCode  = "code"
)

const stateBytes = 32

const stateVersion = 1

type method struct {
	clientID     string
	clientSecret string
	redirectURI  string
	endpoint     oauth2.Endpoint
	apiBase      string
	client       *http.Client
}

var _ authn.LoginMethod = (*method)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It resolves its configuration and the gateway's public address itself.
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
			return prepare(reader)
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
			client := egress.New(egress.Options{AllowLoopback: cfg.allowLoopback, RootCAs: cfg.rootCAs})
			// The restricted client is wrapped outside, so every exchange and read continues the call's trace.
			client.Transport = observability.Transport(log.Transport(client.Transport))
			return newMethod(cfg, client), nil
		},
	}
}

func prepare(reader config.Reader) (core.Enablement, error) {
	idGiven, err := credentialGiven(reader, &clientIDOnly{})
	if err != nil {
		return core.Enablement{}, err
	}
	secretGiven, err := credentialGiven(reader, &clientSecretOnly{})
	if err != nil {
		return core.Enablement{}, err
	}
	switch {
	case !idGiven && !secretGiven:
		return core.Enablement{
			State: core.StateDisabled,
			Reason: "no client-id and no client-secret were given, so no GitHub OAuth application " +
				"is registered and the GitHub sign-in method is not offered",
		}, nil
	case !idGiven:
		return core.Enablement{}, halfConfigured(clientIDItem)
	case !secretGiven:
		return core.Enablement{}, halfConfigured(clientSecretItem)
	}
	if _, err := readConfig(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the OAuth application's credentials and the public base are read "+
			"from configuration, and no reader was handed to the registry before the assembly began: %w",
			ModuleName, err)
	}
	return reader, nil
}

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

// Describe reports the GitHub method: a redirect button that speaks OAuth 2.0
// and opens an account for a GitHub identity no account holds yet.
func (m *method) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             methodName,
		Kind:             authn.MethodKindRedirect,
		Protocol:         authn.MethodProtocolOAuth2,
		Label:            "GitHub",
		OpensAccount:     true,
		BindingProviders: []string{methodName},
	}
}

func (m *method) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     m.clientID,
		ClientSecret: m.clientSecret,
		RedirectURL:  m.redirectURI,
		Endpoint:     m.endpoint,
		Scopes:       scopes,
	}
}

// Begin starts a flow: it keeps the state digest and PKCE verifier in the method state and answers the provider's authorization URL.
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

// Answer handles the redirect back: an error parameter ends the flow, the state is compared to the recorded digest, and the code is exchanged for the identity.
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

func (m *method) identify(ctx context.Context, code, verifier string) authn.Step {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, m.client)
	tok, err := m.oauthConfig().Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
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

func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	}
	return false
}

func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("authn.login.github: purpose %q is not served", purpose))
}

func displayName(u githubUser) string {
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	return strings.TrimSpace(u.Login)
}

func first(in authn.StepInput, key string) string {
	if v := in.Query.Get(key); v != "" {
		return v
	}
	return in.Form.Get(key)
}

var (
	errRefused = fmt.Errorf("authn.login.github: %w", authn.ErrInvalidCredentials)

	errNotCallback   = errors.New("authn.login.github: the answer is not a callback")
	errUnknownStep   = errors.New("authn.login.github: the flow is at a step this method did not name")
	errCancelled     = errors.New("authn.login.github: the provider reported the request was not authorized")
	errBadState      = errors.New("authn.login.github: the flow carries no state to compare")
	errStateMismatch = errors.New("authn.login.github: the state does not match the flow")
	errNoCode        = errors.New("authn.login.github: the callback carries no authorization code")
	errNoSubject     = errors.New("authn.login.github: the provider reported no account number")
)

func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.login.github: %s: %w", doing, err))
}

func readFailure(doing string, err error) authn.Step {
	var pe *providerError
	if errors.As(err, &pe) {
		return authn.StepRefused(errRefused)
	}
	return unavailable(doing, err)
}

type flowState struct {
	Version  int    `json:"v"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
}

func (s flowState) private() []byte {
	b, _ := json.Marshal(s)
	return b
}

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

func readState(private []byte) (flowState, bool) {
	var s flowState
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion || s.State == "" || s.Verifier == "" || dec.More() {
		return flowState{}, false
	}
	return s, true
}

func stateMatches(digestHex, state string) bool {
	want, err := hex.DecodeString(digestHex)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(state))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}
