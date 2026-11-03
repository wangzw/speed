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

// modeItem is the configuration key that chooses the mechanism.
const modeItem = "mode"

// The configuration keys of the Turnstile mechanism. They are configuration and
// not settings because the provider is built at construction, before any store
// is reachable.
const (
	siteKeyItem   = "site-key"
	secretKeyItem = "secret-key"
	verifyURLItem = "verify-url"
	hostnameItem  = "hostname"
	actionItem    = "action"
	timeoutItem   = "timeout"
)

// The closed set of modes. The proof of work is the default: a deployment that
// takes the module in without configuring it gates every send on a proof it
// checks itself, and a deployment that wants no gate states Off.
const (
	// ModeOff leaves the gate open: Required reports false and every proof,
	// an empty one included, is accepted.
	ModeOff = "off"
	// ModePoW is the self-hosted proof of work.
	ModePoW = "pow"
	// ModeTurnstile is Cloudflare Turnstile.
	ModeTurnstile = "turnstile"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Mode is the mechanism the deployment runs, one of the closed set above.
	Mode string `config:"mode"`
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
	// Action, when non-empty, is the action the widget was rendered with; the
	// provider's answer must name it.
	Action string `config:"action"`
	// Timeout bounds one verification as a whole.
	Timeout time.Duration `config:"timeout"`
}

// configDefaults is what the module is worth without a configuration file. The
// gate runs the self-hosted proof of work, which needs no credential and no
// hostname; only the Turnstile endpoint and its timeout have a value, and a
// deployment that turns Turnstile on supplies its own site key, secret and
// hostname.
var configDefaults = moduleConfig{
	Mode:      ModePoW,
	VerifyURL: turnstile.DefaultVerifyURL,
	Timeout:   turnstile.DefaultTimeout,
}

// resolved is the configuration this module runs on.
type resolved struct {
	Mode      string
	siteKey   string
	secretKey string
	verifyURL string
	hostname  string
	action    string
	timeout   time.Duration
}

// resolve holds the mode to the closed set. A value outside it is a
// configuration defect, not a mode to fall back from: silently running off
// would leave a deployment that asked for a gate sending without one, and
// silently running a mechanism it misspelt would ask clients for a proof of a
// kind the deployment did not choose.
func (c moduleConfig) resolve() (resolved, error) {
	switch c.Mode {
	case ModeOff, ModePoW:
		return resolved{Mode: c.Mode}, nil
	case ModeTurnstile:
		return c.resolveTurnstile()
	default:
		return resolved{}, fmt.Errorf("%w: %s.%s is %q, and it has to be %q, %q or %q",
			authn.ErrInvalidConfig, ConfigNamespace, modeItem, c.Mode, ModeOff, ModePoW, ModeTurnstile)
	}
}

// resolveTurnstile holds the Turnstile section to what its provider needs. The
// site key, the secret and the expected hostname are all required: a deployment
// that turns the mechanism on without them cannot check a proof, and starting
// anyway would send messages on a check nobody supplied.
func (c moduleConfig) resolveTurnstile() (resolved, error) {
	siteKey := strings.TrimSpace(c.SiteKey)
	if siteKey == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode renders its widget with it",
			authn.ErrInvalidConfig, ConfigNamespace, siteKeyItem, ModeTurnstile)
	}
	secretKey := strings.TrimSpace(c.SecretKey)
	if secretKey == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode authenticates its siteverify "+
			"call with it", authn.ErrInvalidConfig, ConfigNamespace, secretKeyItem, ModeTurnstile)
	}
	hostname := strings.TrimSpace(c.Hostname)
	if hostname == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the %q mode accepts only a proof the "+
			"provider issued for that hostname", authn.ErrInvalidConfig, ConfigNamespace, hostnameItem,
			ModeTurnstile)
	}
	verifyURL := strings.TrimSpace(c.VerifyURL)
	parsed, err := url.Parse(verifyURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is %q, and the siteverify endpoint is an absolute "+
			"https URL", authn.ErrInvalidConfig, ConfigNamespace, verifyURLItem, c.VerifyURL)
	}
	timeout := c.Timeout
	if timeout < turnstile.TimeoutMin || timeout > turnstile.TimeoutMax {
		return resolved{}, fmt.Errorf("%w: %s.%s is %s, and one verification may take between %s and %s",
			authn.ErrInvalidConfig, ConfigNamespace, timeoutItem, timeout, turnstile.TimeoutMin,
			turnstile.TimeoutMax)
	}
	return resolved{
		Mode:      ModeTurnstile,
		siteKey:   siteKey,
		secretKey: secretKey,
		verifyURL: verifyURL,
		hostname:  hostname,
		action:    strings.TrimSpace(c.Action),
		timeout:   timeout,
	}, nil
}

// schema declares this module's input items. The mode carries a default, so it
// is not required: a section that leaves it out runs the proof of work. The
// Turnstile items are judged by the mode, not by the loader, because a gate that
// runs off or on the proof of work needs none of them.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			modeItem: {
				Description: "the human-verification mechanism a send must satisfy: off, pow or turnstile",
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
				Description: "the optional Turnstile action the widget was rendered with; empty accepts any",
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
