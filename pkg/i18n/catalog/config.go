package catalog

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/i18n"
)

// ErrInvalidConfig reports that the configuration holds a value this module
// cannot run on: a default locale that names no BCP 47 language tag.
var ErrInvalidConfig = errors.New("i18n: the configuration holds a value it cannot run on")

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// DefaultLocale is the locale a request that names none resolves to, and the
	// fallback for a key a catalog misses.
	DefaultLocale string `config:"default-locale"`
}

// configDefaults is what this module is worth without a configuration file.
var configDefaults = moduleConfig{
	DefaultLocale: "en-US",
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	DefaultLocale i18n.Locale
}

// resolve holds the configuration to what the engine can run on: a default
// locale written as a BCP 47 language tag. An empty value names no locale and is
// refused as well, because a default that names nothing has nothing to resolve
// to and would send every fallback to a locale no catalog carries.
func (c moduleConfig) resolve() (resolved, error) {
	if c.DefaultLocale == "" || !i18n.ValidLocaleTag(c.DefaultLocale) {
		return resolved{}, fmt.Errorf("%w: %s.%s is %q, which does not name a BCP 47 language tag",
			ErrInvalidConfig, i18n.ConfigNamespace, i18n.DefaultLocaleItem, c.DefaultLocale)
	}
	return resolved{DefaultLocale: i18n.Locale(c.DefaultLocale)}, nil
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: i18n.ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			i18n.DefaultLocaleItem: {
				Description: "the locale a request that names none resolves to, and the fallback for a key a catalog misses",
			},
		},
	}
}

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(i18n.ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
