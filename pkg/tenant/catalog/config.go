package catalog

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

// refreshItem is the only key this module accepts, relative to its namespace.
const refreshItem = "refresh-period"

// configDefaults is what this module is worth without a configuration file.
//
// Thirty seconds bounds how long a tenant that was just disabled keeps being
// served. It is a compromise between two costs that pull apart: a shorter
// period reads the roster table more often on every replica, and a longer one
// leaves an operator waiting after disabling a tenant. Zero is the way out of
// both, and it means the snapshot is loaded once and never again.
var configDefaults = moduleConfig{
	RefreshPeriod: 30 * time.Second,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// RefreshPeriod is how often the roster is read back into memory. The
	// bound it sets is on how stale an answer may be, not on how long a
	// request waits: reads never touch the table.
	//
	// Zero turns the refresh off. Disabling a tenant then takes effect when
	// the process restarts, which is a legitimate choice for a deployment
	// whose roster does not change while it runs.
	RefreshPeriod time.Duration `config:"refresh-period"`
}

// settings is the configuration this module actually runs on.
type settings struct {
	refresh time.Duration
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on.
func (c moduleConfig) resolve() (settings, error) {
	if c.RefreshPeriod < 0 {
		return settings{}, fmt.Errorf(
			"%s: %s must not be negative, got %s",
			ConfigNamespace, refreshItem, c.RefreshPeriod)
	}
	return settings{refresh: c.RefreshPeriod}, nil
}

// schema declares this module's input items. The list is collected over every
// registered module whether or not it runs, so the namespace has to be this
// module's own name: two modules declaring the same path is a startup failure
// even when only one of them ever runs.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			refreshItem: {
				Description: "how long a tenant's state may lag behind the roster table, " +
					"or zero to load the roster once at startup and never again",
			},
		},
	}
}

// readSettings decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}
