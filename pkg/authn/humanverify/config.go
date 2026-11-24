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

const (
	modeItem      = "mode"
	phoneModeItem = "phone-mode"
)

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

type moduleConfig struct {
	Mode      string        `config:"mode"`
	PhoneMode string        `config:"phone-mode"`
	SiteKey   string        `config:"site-key"`
	SecretKey string        `config:"secret-key"`
	VerifyURL string        `config:"verify-url"`
	Hostname  string        `config:"hostname"`
	Action    string        `config:"action"`
	Timeout   time.Duration `config:"timeout"`
}

var configDefaults = moduleConfig{
	Mode:      ModePoW,
	PhoneMode: ModeCaptcha,
	VerifyURL: turnstile.DefaultVerifyURL,
	Timeout:   turnstile.DefaultTimeout,
}

type surface struct {
	mode      string
	siteKey   string
	secretKey string
	verifyURL string
	hostname  string
	action    string
	timeout   time.Duration
}

type resolved struct {
	other surface
	phone surface
}

func (r resolved) needsStore() bool {
	return storeMode(r.other.mode) || storeMode(r.phone.mode)
}

func storeMode(mode string) bool {
	return mode == ModePoW || mode == ModeCaptcha || mode == ModeTurnstile
}

func (r resolved) usesTurnstile() bool {
	return r.other.mode == ModeTurnstile || r.phone.mode == ModeTurnstile
}

func (r resolved) usesPoW() bool {
	return r.other.mode == ModePoW || r.phone.mode == ModePoW
}

func (r resolved) usesCaptcha() bool {
	return r.other.mode == ModeCaptcha || r.phone.mode == ModeCaptcha
}

func (r resolved) usesSettings() bool {
	return r.usesPoW() || r.usesCaptcha()
}

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

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
