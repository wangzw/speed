package catalog

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

const refreshItem = "refresh-period"

var configDefaults = moduleConfig{
	RefreshPeriod: 30 * time.Second,
}

type moduleConfig struct {
	// RefreshPeriod is how often the roster is read back into memory; it bounds
	// staleness, not request wait, since reads never touch the table. Zero turns
	// the refresh off.
	RefreshPeriod time.Duration `config:"refresh-period"`
}

type settings struct {
	refresh time.Duration
}

func (c moduleConfig) resolve() (settings, error) {
	if c.RefreshPeriod < 0 {
		return settings{}, fmt.Errorf(
			"%s: %s must not be negative, got %s",
			ConfigNamespace, refreshItem, c.RefreshPeriod)
	}
	return settings{refresh: c.RefreshPeriod}, nil
}

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

func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}
