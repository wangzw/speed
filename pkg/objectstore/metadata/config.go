package metadata

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/objectstore"
)

// The keys this module accepts, relative to its namespace, and the full paths a
// failure names.
const (
	endpointsItem = "endpoints"
	sweepItem     = "sweep-interval"

	endpointsKey = ConfigNamespace + "." + endpointsItem
	sweepKey     = ConfigNamespace + "." + sweepItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the object store routes on every
// endpoint, the public ones included, would put them where no gateway guards
// them.
const allEndpoints = "*"

// The default this module is worth without a configuration file: a sweep every
// minute. There is no default for the endpoint list — see configDefaults.
const defaultSweepInterval = time.Minute

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: a list that mounted the routes nowhere
// would silently drop them, and guessing which endpoints should carry them is a
// guess about what is exposed. A run that mounts the routes and cannot answer
// fails the startup, naming the key.
var configDefaults = moduleConfig{
	SweepInterval: defaultSweepInterval,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the routes are mounted on, by the
	// entry point's own names. The list is a set of concrete names: the name that
	// stands for every endpoint is not accepted. It is read where the routes are
	// mounted, so a run that assembles no entry point mounts nothing and leaves it
	// unread.
	Endpoints []string `config:"endpoints"`
	// SweepInterval is how often the background sweep runs. Zero, or negative,
	// turns the sweep off. It sets a startup ticker, so a change requires a
	// restart: it is a configuration item, not a setting.
	SweepInterval time.Duration `config:"sweep-interval"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints     []string
	sweepInterval time.Duration
}

// resolve turns decoded configuration into what the module runs on.
//
// Nothing here is refused: the sweep interval's sign turns the sweep off rather
// than making the run unrunnable. The endpoint list is left to
// validateEndpoints: it says where the routes are mounted, so it is judged where
// the mounting happens, and only when there is an entry point to mount on.
func (c moduleConfig) resolve() (resolved, error) {
	return resolved{
		endpoints:     c.Endpoints,
		sweepInterval: c.SweepInterval,
	}, nil
}

// validateEndpoints refuses an endpoint list the routes cannot be mounted on: an
// empty one, an empty name, the name that stands for every endpoint, or a name
// written twice.
//
// It runs in Init, where the entry point is in hand, and is called only when one
// is present: the list says where the routes are mounted, so a run that mounts
// nothing reads it nowhere and an empty list is a legal configuration there. A
// run that does mount has to name the endpoints, and being refused here is what
// keeps it from mounting nothing silently.
func validateEndpoints(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("%w: %s is empty. The object store routes are mounted on the "+
			"endpoints this module is told to and on none by default; list the endpoints that should "+
			"carry them", objectstore.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		switch name {
		case "":
			return fmt.Errorf("%w: %s holds an empty name at position %d",
				objectstore.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the routes on every endpoint would put them on the public "+
				"ones, where no gateway guards them", objectstore.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every route on it twice", objectstore.ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// schema declares this module's input items. The namespace is this module's own
// name, as it is for every registering module.
//
// Every item takes the primary source alone. The endpoint list decides where the
// routes are mounted and the sweep interval sets a startup ticker, so both are
// what the process exposes and how it runs from the moment it starts: an
// environment variable or a command-line argument that could rewrite either
// would be a way to change that from outside the configuration file. The
// upload window and the object lifetime are runtime tunables and are declared
// as settings instead.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the object store routes are mounted on, by the " +
					"names the entry point's own configuration gives them; concrete names only, never *. " +
					"Required of a run that mounts the routes, and unread by a run that assembles no entry point",
			},
			sweepItem: {
				Origins: config.OriginPrimary,
				Description: "how often the background sweep runs; zero or negative turns the sweep off, " +
					"leaving expired rows until one is read",
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
