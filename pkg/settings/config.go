package settings

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

// refreshItem is the one input item this module accepts, relative to the
// namespace.
const refreshItem = "refresh-interval"

// configDefaults carries the declared defaults: the field values of this
// prototype are what an unset input item resolves to.
var configDefaults = moduleConfig{
	RefreshInterval: 5 * time.Minute,
}

// moduleConfig is the set of input items this module accepts.
//
// There is one, and it has a default, so no command can fail to supply what
// this module needs: the stance is enabled whenever the module is taken in,
// and there is no stand-down to express.
type moduleConfig struct {
	// RefreshInterval is how often the snapshot is reloaded from the table.
	// Zero accepts convergence through change events alone, which means a
	// change this replica never receives stays invisible to it until the
	// process restarts.
	RefreshInterval time.Duration `config:"refresh-interval"`
}

// moduleSettings is the configuration this module runs on.
type moduleSettings struct {
	refresh time.Duration
}

// resolve turns the declared configuration into what the module uses, refusing
// what cannot be honoured.
//
// A negative interval is the one such value: no timer expresses it, and
// accepting it would leave the refresh silently not running.
func (c moduleConfig) resolve() (moduleSettings, error) {
	if c.RefreshInterval < 0 {
		return moduleSettings{}, fmt.Errorf("%w: %s.%s is %s, and a refresh interval cannot be "+
			"negative. Set it to zero to refresh only on change events",
			ErrInvalidValue, ConfigNamespace, refreshItem, c.RefreshInterval)
	}
	return moduleSettings{refresh: c.RefreshInterval}, nil
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
			refreshItem: {
				Description: "how often the settings snapshot is reloaded from the table, " +
					"or zero to rely on change events alone",
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
	return cfg.resolve()
}
