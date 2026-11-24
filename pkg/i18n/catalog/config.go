package catalog

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/i18n"
)

// ErrInvalidConfig reports a configuration value this module cannot run on: a
// default locale that names no BCP 47 language tag.
var ErrInvalidConfig = errors.New("i18n: the configuration holds a value it cannot run on")

const catalogsDirItem = "catalogs-dir"

type moduleConfig struct {
	DefaultLocale string `config:"default-locale"`
	CatalogsDir   string `config:"catalogs-dir"`
}

var configDefaults = moduleConfig{
	DefaultLocale: "en-US",
}

type resolved struct {
	DefaultLocale i18n.Locale
	CatalogsDir   string
}

func (c moduleConfig) resolve() (resolved, error) {
	if c.DefaultLocale == "" || !i18n.ValidLocaleTag(c.DefaultLocale) {
		return resolved{}, fmt.Errorf("%w: %s.%s is %q, which does not name a BCP 47 language tag",
			ErrInvalidConfig, i18n.ConfigNamespace, i18n.DefaultLocaleItem, c.DefaultLocale)
	}
	return resolved{DefaultLocale: i18n.Locale(c.DefaultLocale), CatalogsDir: c.CatalogsDir}, nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: i18n.ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			i18n.DefaultLocaleItem: {
				Description: "the locale a request that names none resolves to, and the fallback for a key a catalog misses",
			},
			catalogsDirItem: {
				Description: "a directory of extra message catalogs, one {tag}.json file per locale, " +
					"read at startup and merged over the catalogs the modules declare; unset means no extras",
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(i18n.ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
