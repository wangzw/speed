// Package wechatmp carries the WeChat official-account login method: the first
// factor that proves a WeChat identity by the code the provider hands back from
// the web authorization an official account's page runs.
//
// It is one authn.LoginMethod among the first factors, registered as
// authn.login.wechat-mp and reporting the name wechat.mp. It delivers the
// login-method capability non-exclusively, so it stands beside every other
// method, and it holds one official account's credentials. It shares the thin
// client — the token exchange, the error sanitizing and the subject rules — with
// the website-application method under login/internal/wechat; the two are
// separate modules with separate credentials because WeChat issues each its own.
//
// A login begins by sending the user agent to the provider's web-authorization
// address with a fresh state; the callback comes back to this capability's
// callback route, where the state is compared and the code is traded for the
// identity it names. The identity carries no email, and the name it carries is
// not an authorization fact: the method calls no user-info endpoint and
// synthesizes nothing.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine and providing this module's two configuration items, which are
// required once the module is named.
package wechatmp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	wechat "github.com/vislake/speed/pkg/authn/login/internal/wechat"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login.wechat-mp"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, flow records and metric attributes, and the callback path
// is built from it, so a published name never changes.
const methodName = "wechat.mp"

// scope is the authorization scope the official account is asked for.
const scope = "snsapi_userinfo"

// stepAuthorize is this method's own name for its single step, the redirect to
// the provider. The engine keeps it in the flow record.
const stepAuthorize = "authorize"

// authorizeEndpoint is the provider endpoint the user agent is sent to. It is
// the web-authorization endpoint, whose address ends with the #wechat_redirect
// fragment the shared client appends.
const authorizeEndpoint = wechat.AuthorizeWebAuth

// The gateway's section, read for the public address and base path the callback
// address is built from.
const (
	gateNamespace = "authn.gate"
	callbackPath  = "/callback/"
)

// The configuration items this module accepts, relative to its namespace.
const (
	appIDItem     = "app-id"
	appSecretItem = "app-secret"
)

// method is this module's product.
type method struct {
	client      *wechat.Client
	redirectURI string
}

// method must satisfy the capability it delivers.
var _ authn.LoginMethod = (*method)(nil)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The two configuration items are required, so a command that names this module
// without them fails at startup with ErrInvalidConfig and the item it needs; the
// login-method capability is delivered without being claimed, which is what lets
// several methods stand side by side. Prepare reads and validates the module's
// section and the gateway's public address ahead of every construction, so a
// deployment missing either is a startup failure rather than a surprise when a
// login begins. The logging capability is taken up as an optional requirement,
// because the application secret is registered as a literal redaction rule and
// an assembly with no logging has nothing to register it with.
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
			// The registration runs before any logger this module would take:
			// an attribute bound before the rule exists is never judged by it.
			if err := registerSecret(reg, cfg.appSecret); err != nil {
				return nil, err
			}
			client := wechat.New(wechat.Options{AppID: cfg.appID, AppSecret: cfg.appSecret})
			return newMethod(client, cfg.redirectURI), nil
		},
	}
}

// newMethod builds the product over a client. Production builds the client from
// this module's configuration; a case passes one pointed at a mock server.
func newMethod(client *wechat.Client, redirectURI string) *method {
	return &method{client: client, redirectURI: redirectURI}
}

// Name is the method's stable name.
func (m *method) Name() string { return methodName }

// Begin starts a flow: it draws a fresh state, keeps its digest as the flow's
// private state, and sends the user agent to the provider's authorization
// address. It reads nothing from the start request.
func (m *method) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err), nil
	}
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	st, err := wechat.NewState()
	if err != nil {
		return authn.StepUnavailable(fmt.Errorf("%s: drawing the flow state: %w", ModuleName, err)), nil
	}
	to := m.client.AuthorizeURL(authorizeEndpoint, scope, m.redirectURI, st.Value())
	return authn.StepRedirect(stepAuthorize, st.Private(), to), nil
}

// Answer handles the provider's callback. It checks the state the callback
// carries against the flow's, then trades the code for the identity it names and
// proves it. A callback whose state or code is wrong, or that reports the
// authorization was cancelled, is refused; a provider that cannot be reached is
// an outage; neither answer carries the provider's own words.
func (m *method) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputCallback {
		return authn.StepRefused(errNotCallback), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	if in.Query.Get("error") != "" {
		return authn.StepRefused(errCancelled), nil
	}
	st, ok := wechat.LoadState(f.Private)
	if !ok || !st.Matches(in.Query.Get("state")) {
		return authn.StepRefused(errStateMismatch), nil
	}
	code := in.Query.Get("code")
	if code == "" {
		return authn.StepRefused(errNoCode), nil
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err), nil
	}

	id, err := m.client.Exchange(ctx, code)
	if err != nil {
		var refusal *wechat.RefusalError
		if errors.As(err, &refusal) {
			// The provider stated a failure: a rejected code, rejected
			// credentials or another refusal, told apart in the cause.
			return authn.StepRefused(fmt.Errorf("%s: %w", ModuleName, err)), nil
		}
		return authn.StepUnavailable(fmt.Errorf("%s: %w", ModuleName, err)), nil
	}
	return authn.StepProven(m.client.Proof(id)), nil
}

// servesPurpose reports whether the method answers a flow of this purpose. A
// WeChat identity is a first factor, so it serves a login, a link that binds the
// identity to an account that is already logged in, and a step-up that falls
// back to a fresh first factor. An enrollment registers a second factor, which a
// redirect to a provider is not, so it is refused.
func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

// The causes the method puts on a refusal. None carries anything the client or
// the provider sent.
var (
	errNotCallback   = errors.New("authn.login.wechat-mp: the answer is not a provider callback")
	errCancelled     = errors.New("authn.login.wechat-mp: the provider reported the authorization was cancelled")
	errStateMismatch = errors.New("authn.login.wechat-mp: the callback state does not match the flow")
	errNoCode        = errors.New("authn.login.wechat-mp: the callback carries no authorization code")
)

// refusePurpose refuses a flow of a purpose the method does not serve.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}

// readerOf takes up the configuration reader. Its absence means the host never
// handed one to the registry, which is core's own failure and is carried out as
// it stands; reading it as "nothing was configured" would lose the method in
// silence.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the application credentials are read from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

// registerSecret registers the application secret as a literal redaction rule,
// so it is masked wherever it turns up in a log line. An assembly with no
// logging has nothing to register it with and registers nothing; this runs in
// New, before any logger this module would take.
func registerSecret(reg *core.Registry, secret string) error {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register the application secret "+
			"with: %w", ModuleName, err)
	}
	literal.Register(delivered.Redaction(), ModuleName+".secret", secret)
	return nil
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// AppID is the application identifier of the official account.
	AppID string `config:"app-id"`
	// AppSecret is its application secret. It travels in the token request's
	// query string and is registered as a literal redaction rule.
	AppSecret string `config:"app-secret"`
}

// configDefaults is what this module is worth without a configuration file.
// There is no default credential: a login under a guessed application is worse
// than a deployment that fails to start.
var configDefaults = moduleConfig{}

// gateMount is the part of the gateway's configuration this module reads: the
// public address and the base path its callback route is mounted under.
type gateMount struct {
	PublicURL string `config:"public-url"`
	BasePath  string `config:"base-path"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	appID       string
	appSecret   string
	redirectURI string
}

// resolve turns decoded configuration into what the module runs on. Both
// credentials and the public address are required; the base path falls back to
// the shared default. The failure names the item and never the secret.
func (c moduleConfig) resolve(g gateMount) (resolved, error) {
	appID := strings.TrimSpace(c.AppID)
	if appID == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the login cannot name its WeChat official account",
			authn.ErrInvalidConfig, ConfigNamespace, appIDItem)
	}
	appSecret := strings.TrimSpace(c.AppSecret)
	if appSecret == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the token exchange carries it",
			authn.ErrInvalidConfig, ConfigNamespace, appSecretItem)
	}
	publicURL := strings.TrimSpace(g.PublicURL)
	if publicURL == "" {
		return resolved{}, fmt.Errorf("%w: %s.mount.public-url is empty, and the WeChat callback "+
			"address the provider is told to return to is built from it", authn.ErrInvalidConfig, gateNamespace)
	}
	basePath := strings.TrimSpace(g.BasePath)
	if basePath == "" {
		basePath = mount.DefaultBasePath
	}
	return resolved{
		appID:       appID,
		appSecret:   appSecret,
		redirectURI: publicURL + basePath + callbackPath + methodName,
	}, nil
}

// schema declares this module's input items. Both credentials are required; the
// secret is sensitive.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			appIDItem: {
				Required:    true,
				Description: "the application identifier of the WeChat official account",
			},
			appSecretItem: {
				Required:    true,
				Sensitive:   true,
				Description: "the application secret of the WeChat official account",
			},
		},
	}
}

// readConfig decodes this module's section and the gateway's public address and
// validates both. Prepare and New both go through it, so the two see one
// configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	var gate gateMount
	if err := reader.Decode(gateNamespace+".mount", &gate); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(gate)
}
