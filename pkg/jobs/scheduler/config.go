package scheduler

import (
	"time"

	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace.
const (
	// tickItem is how often the periodic trigger runs.
	tickItem = "tick"
)

// defaultTick is how often the periodic trigger runs when the host names no
// interval.
const defaultTick = time.Minute

// configDefaults is what this module is worth without a configuration file.
var configDefaults = moduleConfig{Tick: defaultTick}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Tick is how often the periodic trigger runs. A value at or below zero
	// turns the trigger off: no ticker runs and no window is ever enqueued.
	Tick time.Duration `config:"tick"`
}

// moduleSettings is the configuration this module runs on.
type moduleSettings struct {
	// tick is the trigger's interval; at or below zero the trigger is off.
	tick time.Duration
}

// resolve turns the declared configuration into what the module uses. A tick at
// or below zero is not a defect: it is a host turning the trigger off, which is
// a deliberate choice — the periodic declarations then have no effect, and the
// process reports no failure.
func (c moduleConfig) resolve() moduleSettings {
	return moduleSettings{tick: c.Tick}
}

// schema declares this module's input items.
//
// The namespace is this module's own name, as it is for every registering
// module: the list is collected over every registered module whether or not it
// runs, so two modules declaring one path is a startup failure even when only
// one of them ever runs.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			tickItem: {
				// The interval comes from the primary configuration source,
				// the file or remote config centre the locator points at.
				Origins:     config.OriginPrimary,
				Description: "how often the periodic trigger runs, or a value at or below zero to turn it off",
			},
		},
	}
}

// readSettings decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
func readSettings(reader config.Reader) (moduleSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleSettings{}, err
	}
	return cfg.resolve(), nil
}
