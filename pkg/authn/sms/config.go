package sms

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/i18n"
)

// The configuration keys this module accepts, relative to its namespace. They
// are configuration and not settings because the worker pool is sized at
// construction, before a database is reachable.
const (
	workersItem   = "workers"
	queueSizeItem = "queue-size"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Workers is how many goroutines drain the queue and call the sender.
	Workers int `config:"workers"`
	// QueueSize is how many sent messages may wait for a worker.
	QueueSize int `config:"queue-size"`
}

// configDefaults is what this module is worth without a configuration file.
var configDefaults = moduleConfig{
	Workers:   4,
	QueueSize: 1024,
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	Workers   int
	QueueSize int
}

// resolve holds the configuration to the ranges the pool can be built on: at
// least one worker, or nothing would ever drain the queue, and at least one
// queue slot, since a zero-capacity queue would refuse every send before it
// wrote a row and leave the SMS capability silently off.
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

// schema declares this module's input items.
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

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// defaultLocaleFallback is the default language an assembly without the i18n
// engine keeps. It matches the i18n capability's own default, so a run with and
// a run without the engine send in the same language by default.
const defaultLocaleFallback = "en-US"

// i18nConfig carries the one item this module reads from the i18n capability's
// namespace: the default language a request that names none resolves to. It is
// the same item the i18n engine reads, so one statement of the default serves
// both capabilities.
type i18nConfig struct {
	DefaultLocale string `config:"default-locale"`
}

// readDefaultLocale reads the shared default language from the i18n capability's
// namespace. The item is pre-seeded with the fallback, so an assembly that
// declares no i18n engine — and therefore no such namespace — leaves the value
// untouched and the module sends in English, the same default the engine would
// have read.
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
