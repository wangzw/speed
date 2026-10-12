package guard

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/tenant"
)

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
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
// carries no declaration. The set is closed: a route is either refused or let
// through, and the zero value is the refusal, so a value that never went
// through the parse reads closed rather than open.
type undeclaredMode uint8

const (
	undeclaredClosed undeclaredMode = iota
	undeclaredOpen
)

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: a list that guards nothing would let
// every route through, and guessing which endpoints carry authorized traffic is
// a guess about what is enforced. A module that cannot answer fails the
// startup, naming the key. The undeclared disposition defaults to closed, so a
// route nobody declared is refused until it is declared.
var configDefaults = moduleConfig{
	Undeclared: closedValue,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints this module guards, by the entry
	// point's own names. The list is a set of concrete names: the name that
	// stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
	// Undeclared says what a guarded endpoint does with a route that carries no
	// declaration: closed to refuse it, open to let it through.
	Undeclared string `config:"undeclared"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints  []string
	undeclared undeclaredMode
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A refusal names the key: an endpoint list the module
// cannot run on is authn.ErrInvalidConfig, this module having no failure sentinel
// of its own and the authn set being closed, and a name written twice is
// tenant.ErrDuplicateEndpoint — the sentinel the admission layer's own endpoint
// list refuses a duplicate with, because the design gives the two lists the same
// shape and a host tells the same mistake apart the same way.
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
// refused rather than read towards either disposition: the item decides whether
// an undeclared route is refused, so a value the code does not know must not be
// taken as consent.
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

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
//
// Both items take the primary source alone. They decide what is guarded and
// what happens to a route nobody declared, so an environment variable or a
// command-line argument that could rewrite them would be a way to switch the
// enforcement off without touching the configuration file. This is what keeps
// the undeclared disposition out of the override layer: a flag is not a way to
// choose which routes need no permission.
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

// readConfig decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
