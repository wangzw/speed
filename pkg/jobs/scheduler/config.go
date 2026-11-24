package scheduler

import (
	"time"

	"github.com/vislake/speed/pkg/config"
)

const (
	tickItem = "tick"
)

const defaultTick = time.Minute

var configDefaults = moduleConfig{Tick: defaultTick}

type moduleConfig struct {
	Tick time.Duration `config:"tick"`
}

type moduleSettings struct {
	tick time.Duration
}

func (c moduleConfig) resolve() moduleSettings {
	return moduleSettings{tick: c.Tick}
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			tickItem: {
				Origins:     config.OriginPrimary,
				Description: "how often the periodic trigger runs, or a value at or below zero to turn it off",
			},
		},
	}
}

func readSettings(reader config.Reader) (moduleSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleSettings{}, err
	}
	return cfg.resolve(), nil
}
