package settings

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

const refreshItem = "refresh-interval"

var configDefaults = moduleConfig{
	RefreshInterval: 5 * time.Minute,
}

type moduleConfig struct {
	// RefreshInterval is how often the snapshot is reloaded; zero accepts
	// convergence through change events alone.
	RefreshInterval time.Duration `config:"refresh-interval"`
}

type moduleSettings struct {
	refresh time.Duration
}

func (c moduleConfig) resolve() (moduleSettings, error) {
	if c.RefreshInterval < 0 {
		return moduleSettings{}, fmt.Errorf("%w: %s.%s is %s, and a refresh interval cannot be "+
			"negative. Set it to zero to refresh only on change events",
			ErrInvalidValue, ConfigNamespace, refreshItem, c.RefreshInterval)
	}
	return moduleSettings{refresh: c.RefreshInterval}, nil
}

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

func readSettings(reader config.Reader) (moduleSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleSettings{}, err
	}
	return cfg.resolve()
}
