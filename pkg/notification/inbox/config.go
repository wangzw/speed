package inbox

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/notification"
)

const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the inbox routes on every endpoint,
// the public ones included, would put them where no gateway guards them.
const allEndpoints = "*"

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints names the listening endpoints the inbox routes are mounted on,
	// by the entry point's own names. The list is a set of concrete names: the
	// name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

type resolved struct {
	endpoints []string
}

func (c moduleConfig) resolve() resolved { return resolved{endpoints: c.Endpoints} }

func validateEndpoints(endpoints []string) error {
	seen := make(map[string]struct{}, len(endpoints))
	for i, name := range endpoints {
		switch name {
		case "":
			return fmt.Errorf("%w: %s holds an empty name at position %d",
				notification.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete endpoint names "+
				"only: mounting the routes on every endpoint would put them on the public ones, where no "+
				"gateway guards them", notification.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return fmt.Errorf("%w: %s names the endpoint %q twice, which would register every route on "+
				"it twice", notification.ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the inbox routes are mounted on, by the names the " +
					"entry point's own configuration gives them; concrete names only, never *. It is " +
					"read only when the HTTP entry point is present, and then it is required",
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(), nil
}
