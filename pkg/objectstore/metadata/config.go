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
	windowItem    = "upload-window"
	ttlItem       = "object-ttl"
	sweepItem     = "sweep-interval"

	endpointsKey = ConfigNamespace + "." + endpointsItem
	windowKey    = ConfigNamespace + "." + windowItem
	ttlKey       = ConfigNamespace + "." + ttlItem
	sweepKey     = ConfigNamespace + "." + sweepItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the object store routes on every
// endpoint, the public ones included, would put them where no gateway guards
// them.
const allEndpoints = "*"

// The defaults this module is worth without a configuration file: a fifteen
// minute upload window, no expiry on a completed object, and a sweep every
// minute. There is no default for the endpoint list — see configDefaults.
const (
	defaultUploadWindow  = 15 * time.Minute
	defaultObjectTTL     = time.Duration(0)
	defaultSweepInterval = time.Minute
)

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: a list that mounted the routes nowhere
// would silently drop them, and guessing which endpoints should carry them is a
// guess about what is exposed. A run that mounts the routes and cannot answer
// fails the startup, naming the key.
var configDefaults = moduleConfig{
	UploadWindow:  defaultUploadWindow,
	ObjectTTL:     defaultObjectTTL,
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
	// UploadWindow is how long a declared object stays open for upload. It is
	// written into upload_expires_at at declaration, and an upload past it is
	// refused.
	UploadWindow time.Duration `config:"upload-window"`
	// ObjectTTL is how long a completed object lives before the sweep tombstones
	// it. Zero, or negative, means a completed object does not expire.
	ObjectTTL time.Duration `config:"object-ttl"`
	// SweepInterval is how often the background sweep runs. Zero, or negative,
	// turns the sweep off.
	SweepInterval time.Duration `config:"sweep-interval"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints     []string
	uploadWindow  time.Duration
	objectTTL     time.Duration
	sweepInterval time.Duration
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A refusal names the key. The module's own failure
// sentinel for a configuration it cannot run on is objectstore.ErrInvalidConfig.
//
// The endpoint list is left to validateEndpoints: it says where the routes are
// mounted, so it is judged where the mounting happens, and only when there is an
// entry point to mount on.
func (c moduleConfig) resolve() (resolved, error) {
	if c.UploadWindow <= 0 {
		return resolved{}, fmt.Errorf("%w: %s is %s, and an upload window has to be positive: a "+
			"declaration with no window open would refuse every upload",
			objectstore.ErrInvalidConfig, windowKey, c.UploadWindow)
	}
	if c.ObjectTTL < 0 {
		return resolved{}, fmt.Errorf("%w: %s is %s, and an object lifetime cannot be negative; set it "+
			"to zero for a completed object that does not expire",
			objectstore.ErrInvalidConfig, ttlKey, c.ObjectTTL)
	}
	return resolved{
		endpoints:     c.Endpoints,
		uploadWindow:  c.UploadWindow,
		objectTTL:     c.ObjectTTL,
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
// routes are mounted, and the three durations are the object store's own
// policy, so an environment variable or a command-line argument that could
// rewrite any of them would be a way to change what the process exposes from
// outside the configuration file.
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
			windowItem: {
				Origins: config.OriginPrimary,
				Description: "how long a declared object stays open for upload; an upload past this " +
					"window is refused, and the sweep tombstones a pending object whose window has closed",
			},
			ttlItem: {
				Origins: config.OriginPrimary,
				Description: "how long a completed object lives before the sweep tombstones it; zero " +
					"means a completed object does not expire",
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
