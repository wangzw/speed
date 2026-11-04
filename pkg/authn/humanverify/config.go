package humanverify

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/turnstile"
	"github.com/vislake/speed/pkg/config"
)

// The configuration keys that choose a mechanism. Each surface has its own
// choice, so a deployment can run a stronger mechanism on the phone surface and
// a cheaper one on the other surface.
const (
	modeItem      = "mode"
	phoneModeItem = "phone-mode"
)

// The configuration keys of the Turnstile mechanism. They are configuration and
// not settings because the provider is built at construction, before any store
// is reachable. A deployment that selects Turnstile on either surface supplies
// them once; both surfaces then use the same widget and secret.
const (
	siteKeyItem   = "site-key"
	secretKeyItem = "secret-key"
	verifyURLItem = "verify-url"
	hostnameItem  = "hostname"
	actionItem    = "action"
	timeoutItem   = "timeout"
)

// The closed set of modes.
const (
	// ModeOff leaves a surface open: Required reports false for it and every
	// proof, an empty one included, is accepted there.
	ModeOff = "off"
	// ModePoW is the self-hosted proof of work.
	ModePoW = "pow"
	// ModeCaptcha is the self-hosted captcha.
	ModeCaptcha = "captcha"
	// ModeTurnstile is Cloudflare Turnstile.
	ModeTurnstile = "turnstile"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Mode is the mechanism the other surface runs, one of the closed set.
	Mode string `config:"mode"`
	// PhoneMode is the mechanism the phone surface runs, one of the closed set.
	PhoneMode string `config:"phone-mode"`
	// SiteKey is the public Turnstile widget key a client renders the widget
	// with. It is not a credential.
	SiteKey string `config:"site-key"`
	// SecretKey is the Turnstile secret key that authenticates the siteverify
	// call. It is registered as a literal redaction rule.
	SecretKey string `config:"secret-key"`
	// VerifyURL is the siteverify endpoint proofs are posted to.
	VerifyURL string `config:"verify-url"`
	// Hostname is the hostname the widget must have run on, as the provider
	// reports it.
	Hostname string `config:"hostname"`
	// Action is the action the widget is rendered with, and the provider's
	// answer must name it. A surface that runs Turnstile requires it: a proof
	// obtained under one action must not be accepted for another.
	Action string `config:"action"`
	// Timeout bounds one verification as a whole.
	Timeout time.Duration `config:"timeout"`
}

// configDefaults is what the module is worth without a configuration file. The
// phone surface runs the self-hosted captcha and the other surface runs the
// self-hosted proof of work: both need no credential and no hostname, so the
// default deployment gates every send without reaching a third party. Only the
// Turnstile endpoint and its timeout have a value, and a deployment that turns
// Turnstile on supplies its own site key, secret, hostname and action.
var configDefaults = moduleConfig{
	Mode:      ModePoW,
	PhoneMode: ModeCaptcha,
	VerifyURL: turnstile.DefaultVerifyURL,
	Timeout:   turnstile.DefaultTimeout,
}

// surface is one surface's resolved mechanism and, when it is Turnstile, the
// credentials it runs with.
type surface struct {
	mode      string
	siteKey   string
	secretKey string
	verifyURL string
	hostname  string
	action    string
	timeout   time.Duration
}

// resolved is the configuration this module runs on: one mechanism per surface.
type resolved struct {
	other surface
	phone surface
}

// needsStore reports whether either surface runs a mechanism that needs the
// key-value store: the self-hosted mechanisms keep their challenges there, and
// Turnstile records the digest of every token it accepts there so a replay is
// refused locally.
func (r resolved) needsStore() bool {
	return storeMode(r.other.mode) || storeMode(r.phone.mode)
}

// storeMode reports whether mode needs the key-value store.
func storeMode(mode string) bool {
	return mode == ModePoW || mode == ModeCaptcha || mode == ModeTurnstile
}

// usesTurnstile reports whether either surface runs Turnstile, which is what
// decides whether a secret is registered for redaction.
func (r resolved) usesTurnstile() bool {
	return r.other.mode == ModeTurnstile || r.phone.mode == ModeTurnstile
}

// usesPoW reports whether either surface runs the proof of work.
func (r resolved) usesPoW() bool {
	return r.other.mode == ModePoW || r.phone.mode == ModePoW
}

// usesCaptcha reports whether either surface runs the self-hosted captcha.
func (r resolved) usesCaptcha() bool {
	return r.other.mode == ModeCaptcha || r.phone.mode == ModeCaptcha
}

// usesSettings reports whether either surface runs a self-hosted mechanism,
// which is what decides whether the settings store is taken up for its
// issuance limits. Turnstile reads no setting.
func (r resolved) usesSettings() bool {
	return r.usesPoW() || r.usesCaptcha()
}

// resolve holds each surface's mode to the closed set and resolves the Turnstile
// credentials once for every surface that selects it. A value outside the set is
// a configuration defect, not a mode to fall back from: silently running off
// would leave a deployment that asked for a gate sending without one, and
// silently running a mechanism it misspelt would ask clients for a proof of a
// kind the deployment did not choose.
func (c moduleConfig) resolve() (resolved, error) {
	other, err := c.resolveSurface(c.Mode, modeItem)
	if err != nil {
		return resolved{}, err
	}
	phone, err := c.resolveSurface(c.PhoneMode, phoneModeItem)
	if err != nil {
		return resolved{}, err
	}
	return resolved{other: other, phone: phone}, nil
}

// resolveSurface resolves one surface's mode. item names the configuration key
// the mode came from, so a failure points at the surface to correct.
func (c moduleConfig) resolveSurface(mode, item string) (surface, error) {
	switch mode {
	case ModeOff, ModePoW, ModeCaptcha:
		return surface{mode: mode}, nil
	case ModeTurnstile:
		return c.resolveTurnstile(item)
	default:
		return surface{}, fmt.Errorf("%w: %s.%s is %q, and it has to be %q, %q, %q or %q",
			authn.ErrInvalidConfig, ConfigNamespace, item, mode, ModeOff, ModePoW, ModeCaptcha, ModeTurnstile)
	}
}

// resolveTurnstile holds the Turnstile section to what its provider needs for
// the surface selected by item. The site key, the secret, the expected hostname
// and the action are all required: a deployment that turns the mechanism on
// without them cannot check a proof, and starting anyway would send messages on
// a check nobody supplied. The action is part of the check because a token
// obtained under one action must not be accepted for another.
func (c moduleConfig) resolveTurnstile(item string) (surface, error) {
	siteKey := strings.TrimSpace(c.SiteKey)
	if siteKey == "" {
		return surface{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode named by %s.%s renders its "+
			"widget with it", authn.ErrInvalidConfig, ConfigNamespace, siteKeyItem, ModeTurnstile,
			ConfigNamespace, item)
	}
	secretKey := strings.TrimSpace(c.SecretKey)
	if secretKey == "" {
		return surface{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode named by %s.%s authenticates "+
			"its siteverify call with it", authn.ErrInvalidConfig, ConfigNamespace, secretKeyItem,
			ModeTurnstile, ConfigNamespace, item)
	}
	hostname := strings.TrimSpace(c.Hostname)
	if hostname == "" {
		return surface{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode named by %s.%s accepts only "+
			"a proof the provider issued for that hostname", authn.ErrInvalidConfig, ConfigNamespace,
			hostnameItem, ModeTurnstile, ConfigNamespace, item)
	}
	action := strings.TrimSpace(c.Action)
	if action == "" {
		return surface{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode named by %s.%s renders its "+
			"widget with it and accepts only a proof the provider issued for that action",
			authn.ErrInvalidConfig, ConfigNamespace, actionItem, ModeTurnstile, ConfigNamespace, item)
	}
	verifyURL := strings.TrimSpace(c.VerifyURL)
	parsed, err := url.Parse(verifyURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return surface{}, fmt.Errorf("%w: %s.%s is %q, and the siteverify endpoint is an absolute "+
			"https URL", authn.ErrInvalidConfig, ConfigNamespace, verifyURLItem, c.VerifyURL)
	}
	timeout := c.Timeout
	if timeout < turnstile.TimeoutMin || timeout > turnstile.TimeoutMax {
		return surface{}, fmt.Errorf("%w: %s.%s is %s, and one verification may take between %s and %s",
			authn.ErrInvalidConfig, ConfigNamespace, timeoutItem, timeout, turnstile.TimeoutMin,
			turnstile.TimeoutMax)
	}
	return surface{
		mode:      ModeTurnstile,
		siteKey:   siteKey,
		secretKey: secretKey,
		verifyURL: verifyURL,
		hostname:  hostname,
		action:    action,
		timeout:   timeout,
	}, nil
}

// schema declares this module's input items. Both mode keys carry a default, so
// neither is required: a section that leaves them out runs the proof of work on
// the other surface and the captcha on the phone surface. The Turnstile items
// are judged by the modes, not by the loader, because a surface that runs off,
// the proof of work or the captcha needs none of them.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			modeItem: {
				Description: "the human-verification mechanism the other surface (every send that does " +
					"not address a phone number) must satisfy: off, pow, captcha or turnstile",
			},
			phoneModeItem: {
				Description: "the human-verification mechanism the phone surface (SMS login, second " +
					"factor, enrollment and anonymous reset) must satisfy: off, pow, captcha or turnstile",
			},
			siteKeyItem: {
				Description: "the public Turnstile site key a client-side widget is rendered with",
			},
			secretKeyItem: {
				Sensitive:   true,
				Description: "the Turnstile secret key that authenticates the siteverify call",
			},
			verifyURLItem: {
				Description: "the Turnstile siteverify endpoint proofs are posted to",
			},
			hostnameItem: {
				Description: "the hostname the Turnstile widget must have run on",
			},
			actionItem: {
				Description: "the Turnstile action the widget is rendered with; required when a surface " +
					"runs turnstile, and the provider's answer must name it",
			},
			timeoutItem: {
				Description: "the whole time one Turnstile verification may take",
			},
		},
	}
}

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
