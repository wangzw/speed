package policy

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// The configuration item this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	loadTimeoutItem = "load-timeout"
	loadTimeoutKey  = ConfigNamespace + "." + loadTimeoutItem
)

// The settings item this module reads at runtime, relative to its namespace,
// and the full path a read goes by.
const (
	refreshIntervalItem = "refresh-interval"
	refreshIntervalKey  = ConfigNamespace + "." + refreshIntervalItem
)

// configDefaults is what this module is worth without a configuration file. The
// load timeout has a default, so no command can fail to supply what this module
// needs by omitting it.
var configDefaults = moduleConfig{
	LoadTimeout: 5 * time.Second,
}

// moduleConfig is the set of input items this module accepts.
//
// The load timeout is configuration rather than a setting because it is read
// before the snapshot exists: a value that had to come from the database could
// not bound the read of the snapshot that comes out of it.
type moduleConfig struct {
	// LoadTimeout bounds the startup load of the whole policy. A load that has
	// not finished within it fails the startup: a process that began serving
	// with a half-read snapshot would answer from a policy it cannot vouch for.
	LoadTimeout time.Duration `config:"load-timeout"`
}

// resolution is the configuration this module runs on.
type resolution struct {
	loadTimeout time.Duration
}

// resolve turns the declared configuration into what the module uses. A
// non-positive timeout is refused: no load finishes in zero time, so accepting
// it would fail every startup, and a negative one names no duration at all.
func (c moduleConfig) resolve() (resolution, error) {
	if c.LoadTimeout <= 0 {
		return resolution{}, fmt.Errorf("%w: %s is %s, and the load of the whole policy must be "+
			"allowed a positive time to finish. Set it to the longest a load may take",
			authn.ErrInvalidConfig, loadTimeoutKey, c.LoadTimeout)
	}
	return resolution{loadTimeout: c.LoadTimeout}, nil
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
		},
	}
}

// options carries the runtime parameters and their declared defaults. It is the
// prototype the settings module reads the defaults off; it never receives a
// value.
type options struct {
	// RefreshInterval is how often the snapshot is reloaded from the table when
	// no event bus carries a change. Zero means no periodic reload: a change
	// this replica never hears about stays invisible until it restarts.
	RefreshInterval time.Duration `config:"refresh-interval"`
}

// settingsDefaults is the prototype for the refresh interval, defaulting to
// off, so that an assembly with a bus relies on events alone.
var settingsDefaults = options{}

// refreshInDomain is the one statement of the interval's range: a reload
// interval is a duration and cannot be negative, and zero is the "off" value
// the design gives it.
func refreshInDomain(d time.Duration) bool { return d >= 0 }

// settingsSchema declares this module's runtime parameters, with the domain the
// code acts on.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &settingsDefaults}},
		Items: map[string]settings.Item{
			refreshIntervalItem: {
				Description: "how often the policy snapshot is reloaded from the table when no " +
					"event bus carries a change, or zero to rely on change events alone",
				Domain: settings.DomainOf("non-negative durations", refreshInDomain),
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
