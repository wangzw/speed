package sms

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/i18n"
)

const (
	workersItem   = "workers"
	queueSizeItem = "queue-size"
)

type moduleConfig struct {
	Workers   int `config:"workers"`
	QueueSize int `config:"queue-size"`
}

var configDefaults = moduleConfig{
	Workers:   4,
	QueueSize: 1024,
}

type resolved struct {
	Workers   int
	QueueSize int
}

func (c moduleConfig) resolve() (resolved, error) {
	if c.Workers < 1 {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d, and at least one worker is needed to drain the queue",
			authn.ErrInvalidConfig, ConfigNamespace, workersItem, c.Workers)
	}
	if c.QueueSize < 1 {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d, and at least one queue slot is needed or every send is refused",
			authn.ErrInvalidConfig, ConfigNamespace, queueSizeItem, c.QueueSize)
	}
	return resolved(c), nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			workersItem: {
				Description: "how many goroutines drain the sending queue and call the carrier",
			},
			queueSizeItem: {
				Description: "how many sent messages may wait for a worker before a send is refused",
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

const defaultLocaleFallback = "en-US"

type i18nConfig struct {
	DefaultLocale string `config:"default-locale"`
}

func readDefaultLocale(reader config.Reader) (string, error) {
	cfg := i18nConfig{DefaultLocale: defaultLocaleFallback}
	if err := reader.Decode(i18n.ConfigNamespace, &cfg); err != nil {
		return "", err
	}
	if cfg.DefaultLocale == "" {
		return defaultLocaleFallback, nil
	}
	return cfg.DefaultLocale, nil
}
