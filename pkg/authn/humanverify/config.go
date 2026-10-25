package humanverify

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// modeItem is the configuration key that chooses the mechanism.
const modeItem = "mode"

// The closed set of modes. Off is the default: a deployment that takes the
// module in without configuring it keeps sending without a proof.
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
}

// configDefaults is what the module is worth without a configuration file.
var configDefaults = moduleConfig{Mode: ModeOff}

// resolved is the configuration this module runs on.
type resolved struct {
	Mode string
}

// resolve holds the mode to the closed set. A value outside it is a
// configuration defect, not a mode to fall back from: silently running off
// would leave a deployment that asked for a gate sending without one, and
// silently running a mechanism it misspelt would ask clients for a proof of a
// kind the deployment did not choose.
func (c moduleConfig) resolve() (resolved, error) {
	switch c.Mode {
	case ModeOff, ModePoW, ModeTurnstile:
		return resolved(c), nil
	default:
		return resolved{}, fmt.Errorf("%w: %s.%s is %q, and it has to be %q, %q or %q",
			authn.ErrInvalidConfig, ConfigNamespace, modeItem, c.Mode, ModeOff, ModePoW, ModeTurnstile)
	}
}

// schema declares this module's input items. The mode carries a default, so it
// is not required: a section that leaves it out runs the gate off.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			modeItem: {
				Description: "the human-verification mechanism a send must satisfy: off, pow or turnstile",
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
