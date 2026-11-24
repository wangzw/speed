// Package wechatweb carries the WeChat website-application login method, which proves a WeChat identity by the code the provider returns after a QR-code login.
package wechatweb

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
const ModuleName = "authn.login.wechat-web"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const methodName = "wechat.web"

const scope = "snsapi_login"

const stepAuthorize = "authorize"

const authorizeEndpoint = wechat.AuthorizeQRConnect

const (
	gateNamespace = "authn.gate"
	callbackPath  = "/callback/"
)

const (
	appIDItem     = "app-id"
	appSecretItem = "app-secret"
)

type method struct {
	client      *wechat.Client
	redirectURI string
}

var _ authn.LoginMethod = (*method)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. The login-method capability is delivered non-exclusively; settings and logging are optional.
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
			if err := registerSecret(reg, cfg.appSecret); err != nil {
				return nil, err
			}
			client := wechat.New(wechat.Options{AppID: cfg.appID, AppSecret: cfg.appSecret})
			return newMethod(client, cfg.redirectURI), nil
		},
	}
}

func newMethod(client *wechat.Client, redirectURI string) *method {
	return &method{client: client, redirectURI: redirectURI}
}

// Name is the method's stable name.
func (m *method) Name() string { return methodName }

// Describe reports the WeChat web method: a redirect button that speaks OAuth
// 2.0 and opens an account for a WeChat identity no account holds yet.
func (m *method) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             methodName,
		Kind:             authn.MethodKindRedirect,
		Protocol:         authn.MethodProtocolOAuth2,
		Label:            "WeChat",
		OpensAccount:     true,
		BindingProviders: wechat.BindingProviders(),
	}
}

// Begin starts a flow: it draws a fresh state, keeps its digest as the private state, and sends the user agent to the authorization address.
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

// Answer checks the callback's state against the flow's and trades the code for the identity it names.
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
			return authn.StepRefused(fmt.Errorf("%s: %w", ModuleName, err)), nil
		}
		return authn.StepUnavailable(fmt.Errorf("%s: %w", ModuleName, err)), nil
	}
	return authn.StepProven(m.client.Proof(id)), nil
}

func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

var (
	errNotCallback   = errors.New("authn.login.wechat-web: the answer is not a provider callback")
	errCancelled     = errors.New("authn.login.wechat-web: the provider reported the authorization was cancelled")
	errStateMismatch = errors.New("authn.login.wechat-web: the callback state does not match the flow")
	errNoCode        = errors.New("authn.login.wechat-web: the callback carries no authorization code")
)

func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the application credentials are read from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

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

type moduleConfig struct {
	AppID     string `config:"app-id"`
	AppSecret string `config:"app-secret"`
}

var configDefaults = moduleConfig{}

type gateMount struct {
	PublicURL string `config:"public-url"`
	Bases     struct {
		Default string `config:"default"`
	} `config:"bases"`
}

type resolved struct {
	appID       string
	appSecret   string
	redirectURI string
}

type appIDOnly struct {
	AppID string `config:"app-id"`
}

type appSecretOnly struct {
	AppSecret string `config:"app-secret"`
}

func credentialGiven(reader config.Reader, target any) (bool, error) {
	err := reader.Decode(ConfigNamespace, target)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, config.ErrMissingRequired):
		return false, nil
	default:
		return false, err
	}
}

func halfConfigured(item string) error {
	return fmt.Errorf("%w: %s.%s was not given while the other credential was; the website "+
		"application needs both the application identifier and the secret, so give both or neither",
		authn.ErrInvalidConfig, ConfigNamespace, item)
}

func prepare(reader config.Reader) (core.Enablement, error) {
	idGiven, err := credentialGiven(reader, &appIDOnly{})
	if err != nil {
		return core.Enablement{}, err
	}
	secretGiven, err := credentialGiven(reader, &appSecretOnly{})
	if err != nil {
		return core.Enablement{}, err
	}
	switch {
	case !idGiven && !secretGiven:
		return core.Enablement{
			State: core.StateDisabled,
			Reason: "no app-id and no app-secret were given, so no WeChat website application is " +
				"registered and the website sign-in method is not offered",
		}, nil
	case !idGiven:
		return core.Enablement{}, halfConfigured(appIDItem)
	case !secretGiven:
		return core.Enablement{}, halfConfigured(appSecretItem)
	}
	if _, err := readConfig(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func (c moduleConfig) resolve(g gateMount) (resolved, error) {
	appID := strings.TrimSpace(c.AppID)
	if appID == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the login cannot name its WeChat application",
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
	if _, err := mount.Origin(publicURL); err != nil {
		return resolved{}, fmt.Errorf("%w: %s.mount.public-url is not an origin: %w",
			authn.ErrInvalidConfig, gateNamespace, err)
	}
	basePath := strings.TrimSpace(g.Bases.Default)
	if basePath == "" {
		basePath = mount.DefaultBasePath
	}
	return resolved{
		appID:       appID,
		appSecret:   appSecret,
		redirectURI: publicURL + basePath + callbackPath + methodName,
	}, nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			appIDItem: {
				Required:    true,
				Description: "the application identifier of the WeChat website application",
			},
			appSecretItem: {
				Required:    true,
				Sensitive:   true,
				Description: "the application secret of the WeChat website application",
			},
		},
	}
}

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
