package policy

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The configuration items this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	loadTimeoutItem     = "load-timeout"
	loadTimeoutKey      = ConfigNamespace + "." + loadTimeoutItem
	refreshIntervalItem = "refresh-interval"
	refreshIntervalKey  = ConfigNamespace + "." + refreshIntervalItem
)

// configDefaults is what this module is worth without a configuration file. The
// load timeout has a default, so no command can fail to supply what this module
// needs by omitting it. The refresh interval defaults to zero, which turns the
// periodic reload off, so an assembly with an event bus relies on events alone.
var configDefaults = moduleConfig{
	LoadTimeout: 5 * time.Second,
}

// moduleConfig is the set of input items this module accepts.
//
// Both items are configuration rather than settings. The load timeout is read
// before the snapshot exists: a value that had to come from the database could
// not bound the read of the snapshot that comes out of it. The refresh interval
// sizes the ticker a startup raises, so a change to it takes a restart anyway.
type moduleConfig struct {
	// LoadTimeout bounds the startup load of the whole policy. A load that has
	// not finished within it fails the startup: a process that began serving
	// with a half-read snapshot would answer from a policy it cannot vouch for.
	LoadTimeout time.Duration `config:"load-timeout"`

	// RefreshInterval is how often the snapshot is reloaded from the table when
	// no event bus carries a change. Zero means no periodic reload: a change
	// this replica never hears about stays invisible until it restarts.
	RefreshInterval time.Duration `config:"refresh-interval"`
}

// resolution is the configuration this module runs on.
type resolution struct {
	loadTimeout     time.Duration
	refreshInterval time.Duration
}

// resolve turns the declared configuration into what the module uses. A
// non-positive timeout is refused: no load finishes in zero time, so accepting
// it would fail every startup, and a negative one names no duration at all. A
// negative refresh interval is refused for the same reason; zero is the "off"
// value the item is given.
func (c moduleConfig) resolve() (resolution, error) {
	if c.LoadTimeout <= 0 {
		return resolution{}, fmt.Errorf("%w: %s is %s, and the load of the whole policy must be "+
			"allowed a positive time to finish. Set it to the longest a load may take",
			authn.ErrInvalidConfig, loadTimeoutKey, c.LoadTimeout)
	}
	if c.RefreshInterval < 0 {
		return resolution{}, fmt.Errorf("%w: %s is %s, and a periodic reload cannot run on a "+
			"negative interval. Set it to zero to turn the periodic reload off",
			authn.ErrInvalidConfig, refreshIntervalKey, c.RefreshInterval)
	}
	return resolution{loadTimeout: c.LoadTimeout, refreshInterval: c.RefreshInterval}, nil
}

// schema declares this module's input items. The namespace is this module's own
// name, as it is for every registering module.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			loadTimeoutItem: {
				Description: "how long the startup load of the whole policy may take before the " +
					"startup fails",
			},
			refreshIntervalItem: {
				Description: "how often the policy snapshot is reloaded from the table when no " +
					"event bus carries a change, or zero to rely on change events alone",
			},
		},
	}
}

// readConfig decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readConfig(reader config.Reader) (resolution, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolution{}, err
	}
	return cfg.resolve()
}
