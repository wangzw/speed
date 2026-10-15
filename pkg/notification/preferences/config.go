package preferences

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/notification"
)

// The keys this module accepts, relative to its namespace, and the full path a
// failure names.
const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the preference routes on every
// endpoint, the public ones included, would put them where no gateway guards
// them, and an endpoint whose whole path space a catch-all already covers
// cannot take them at all.
const allEndpoints = "*"

// configDefaults is what this module is worth without a configuration file.
//
// There is no endpoint list in it. The routes are mounted on the endpoints the
// module is told to and on none by default: the list is read only when the HTTP
// entry point is present, and a host that assembles one has to say which
// endpoints carry the preference routes rather than have them guessed.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the preference routes are mounted
	// on, by the entry point's own names. The list is a set of concrete names:
	// the name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on. An empty
// list is legal: a command with no HTTP entry point mounts no routes and reads
// no endpoint list at all.
func (c moduleConfig) resolve() resolved { return resolved{endpoints: c.Endpoints} }

// validateEndpoints checks the endpoint list. It runs only when the routes are
// mounted, where the list is read: a command with no entry point mounts nothing
// and reads no list, so an endpoint list it never uses is not judged. A refusal
// names the key, and the module's own failure sentinel for a configuration it
// cannot run on is notification.ErrInvalidConfig.
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

// schema declares this module's input items. The namespace is this module's own
// name, as it is for every registering module.
//
// The endpoint list takes the primary source alone. It decides where the routes
// are mounted, so an environment variable or a command-line argument that could
// rewrite it would be a way to move them onto an endpoint from outside the
// configuration file.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the preference routes are mounted on, by the names " +
					"the entry point's own configuration gives them; concrete names only, never *. It is " +
					"read only when the HTTP entry point is present, and then it is required",
			},
		},
	}
}

// readConfig decodes this module's section. New goes through it, so the list the
// mounting reads is the one the configuration gave.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(), nil
}
