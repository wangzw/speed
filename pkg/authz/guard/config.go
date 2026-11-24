package guard

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/tenant"
)

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const (
	endpointsItem  = "endpoints"
	undeclaredItem = "undeclared"

	endpointsKey  = ConfigNamespace + "." + endpointsItem
	undeclaredKey = ConfigNamespace + "." + undeclaredItem
)

const (
	closedValue = "closed"
	openValue   = "open"
)

const allEndpoints = "*"

type undeclaredMode uint8

const (
	undeclaredClosed undeclaredMode = iota
	undeclaredOpen
)

var configDefaults = moduleConfig{
	Undeclared: closedValue,
}

type moduleConfig struct {
	Endpoints  []string `config:"endpoints"`
	Undeclared string   `config:"undeclared"`
}

type resolved struct {
	endpoints  []string
	undeclared undeclaredMode
}

func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The guard enforces the endpoints it "+
			"is told to and none by default; list the endpoints that carry authorized traffic, or "+
			"leave this module out of the command", authn.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This layer takes "+
				"concrete endpoint names only: guarding every endpoint would put an authorization "+
				"decision on the entry point's own routes, the ones a caller reaches before any "+
				"policy applies, and refuse them",
				authn.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would "+
				"register the guard layer on it twice and decide every request it carries twice",
				tenant.ErrDuplicateEndpoint, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	undeclared, err := undeclaredModeOf(c.Undeclared)
	if err != nil {
		return resolved{}, err
	}
	return resolved{endpoints: c.Endpoints, undeclared: undeclared}, nil
}

func undeclaredModeOf(value string) (undeclaredMode, error) {
	switch value {
	case closedValue:
		return undeclaredClosed, nil
	case openValue:
		return undeclaredOpen, nil
	default:
		return 0, fmt.Errorf("%w: %s is %q, want %q or %q. The value decides whether a route "+
			"with no declaration is refused; the safe default is %q",
			authn.ErrInvalidConfig, undeclaredKey, value, closedValue, openValue, closedValue)
	}
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints whose requests are authorized, by the names " +
					"the entry point's own configuration gives them; concrete names only, never *",
			},
			undeclaredItem: {
				Origins: config.OriginPrimary,
				Description: "what a guarded endpoint does with a route that carries no " +
					"declaration: closed to refuse it with 403, or open to let it through; closed " +
					"by default",
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
