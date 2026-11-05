package admin

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// ssoEnabledItem is the input item, relative to the namespace, that gates the
// SSO provider group. Its full path is authn.admin.sso.enabled.
const ssoEnabledItem = "sso.enabled"

// ssoConfig is the part of this module's section that gates the SSO provider
// group.
type ssoConfig struct {
	// Enabled states whether this run mounts the SSO provider group and reports
	// its two features. The group otherwise follows the register capability,
	// which says only that the process could manage providers, not that this
	// deployment offers the management surface.
	Enabled bool `config:"enabled"`
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	SSO ssoConfig `config:"sso"`
}

// configDefaults is what this module is worth without a configuration file: the
// SSO provider group is off. A deployment turns it on by naming the item; an
// absent item reads as this default, so a host that never writes the section
// mounts the account and membership groups alone.
var configDefaults = moduleConfig{SSO: ssoConfig{Enabled: false}}

// configSchema declares this module's input items.
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

// readConfig decodes this module's section.
func readConfig(reader config.Reader) (moduleConfig, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleConfig{}, err
	}
	return cfg, nil
}

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the SSO provider group is gated by configuration, and no reader "+
			"was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}
