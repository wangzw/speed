package guard

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/tenant"
)

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// The configuration items this module accepts, relative to its namespace, and
// the full paths a failure names.
const (
	endpointsItem  = "endpoints"
	undeclaredItem = "undeclared"

	endpointsKey  = ConfigNamespace + "." + endpointsItem
	undeclaredKey = ConfigNamespace + "." + undeclaredItem
)

// The two values the undeclared item takes, as written in configuration.
const (
	closedValue = "closed"
	openValue   = "open"
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it.
const allEndpoints = "*"

// undeclaredMode is the disposition a guarded endpoint gives a route that
// carries no declaration. The zero value is the refusal.
type undeclaredMode uint8

const (
	undeclaredClosed undeclaredMode = iota
	undeclaredOpen
)

// configDefaults is what this module is worth without a configuration file.
// There is no default for the endpoints: a list that guards nothing would let
// every route through. The undeclared disposition defaults to closed.
var configDefaults = moduleConfig{
	Undeclared: closedValue,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	Endpoints  []string `config:"endpoints"`
	Undeclared string   `config:"undeclared"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints  []string
	undeclared undeclaredMode
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. An endpoint list the module cannot run on is
// authn.ErrInvalidConfig; a name written twice is tenant.ErrDuplicateEndpoint.
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
		// The list is a set. The entry point appends a layer to whatever is
		// already on an endpoint, so a name written twice would decide every
		// request the endpoint carries twice.
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

// undeclaredModeOf reads the undeclared item. A value outside the closed set is
// refused rather than read towards either disposition.
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

// schema declares this module's input items. Both items take the primary source
// alone: an environment variable or command-line argument that could rewrite
// them would switch enforcement off without touching the configuration file.
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

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
