package admin

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const ssoEnabledItem = "sso.enabled"

type ssoConfig struct {
	Enabled bool `config:"enabled"`
}

type moduleConfig struct {
	SSO ssoConfig `config:"sso"`
}

var configDefaults = moduleConfig{SSO: ssoConfig{Enabled: false}}

func configSchema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			ssoEnabledItem: {
				Origins: config.OriginPrimary,
				Description: "whether this run mounts the management routes of the SSO provider group; " +
					"off, no such route exists and neither authn.admin.sso nor authn.admin.sso.metadata " +
					"is reported, whatever the process's SSO register holds",
			},
		},
	}
}

func readConfig(reader config.Reader) (moduleConfig, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleConfig{}, err
	}
	return cfg, nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the SSO provider group is gated by configuration, and no reader "+
			"was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}
