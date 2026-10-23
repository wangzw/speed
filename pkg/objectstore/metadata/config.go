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
	contentEndpointsItem = "content-endpoints"
	publicURLItem        = "public-url"
	sweepItem            = "sweep-interval"

	contentEndpointsKey = ConfigNamespace + "." + contentEndpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the content route on every endpoint,
// the public ones included, would put it where the wrong layers guard it and
// where it can be reached by more than the holder of a token.
const allEndpoints = "*"

// The default this module is worth without a configuration file: a sweep every
// minute. There is no default for the endpoint list — see configDefaults — and
// none for the public URL, whose empty value is a same-origin deployment.
const defaultSweepInterval = time.Minute

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the content endpoints: a list that mounted the route
// nowhere would mint addresses nobody can use, and guessing which endpoints
// should carry it is a guess about what is exposed. A run that mounts the route
// and cannot answer fails the startup, naming the key.
var configDefaults = moduleConfig{
	SweepInterval: defaultSweepInterval,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// ContentEndpoints names the listening endpoints the content route is
	// mounted on, by the entry point's own names. The list is a set of concrete
	// names: the name that stands for every endpoint is not accepted. It is read
	// where the route is mounted, so a run with no entry point, or one whose
	// backend mints its own addresses, mounts nothing and leaves it unread.
	ContentEndpoints []string `config:"content-endpoints"`
	// PublicURL is the external origin a minted content address is prefixed
	// with, without a trailing slash. An empty value answers a root-relative
	// address, which a same-origin deployment reaches directly; a cross-origin
	// deployment has to set it. It is deployment identity rather than a runtime
	// tunable, so it is configuration and not a setting.
	PublicURL string `config:"public-url"`
	// SweepInterval is how often the background sweep runs. Zero, or negative,
	// turns the sweep off. It sets a startup ticker, so a change requires a
	// restart: it is a configuration item, not a setting.
	SweepInterval time.Duration `config:"sweep-interval"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	contentEndpoints []string
	publicURL        string
	sweepInterval    time.Duration
}

// resolve turns decoded configuration into what the module runs on.
//
// Nothing here is refused: the sweep interval's sign turns the sweep off rather
// than making the run unrunnable. The endpoint list is left to
// validateContentEndpoints: it says where the route is mounted, so it is judged
// where the mounting happens, and only when there is a route to mount. The
// public URL is used as it stands in contentURL, which removes trailing
// slashes.
func (c moduleConfig) resolve() (resolved, error) {
	return resolved{
		contentEndpoints: c.ContentEndpoints,
		publicURL:        c.PublicURL,
		sweepInterval:    c.SweepInterval,
	}, nil
}

// validateContentEndpoints refuses an endpoint list the content route cannot be
// mounted on: an empty one, an empty name, the name that stands for every
// endpoint, or a name written twice.
//
// It runs in Init, where the entry point is in hand, and is called only when the
// content route is about to be mounted: the list says where that route goes, so
// a run that mounts nothing reads it nowhere and an empty list is a legal
// configuration there. A run that does mount has to name the endpoints, and
// being refused here is what keeps it from minting unusable addresses silently.
func validateContentEndpoints(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("%w: %s is empty. The content route is mounted on the endpoints this module "+
			"is told to and on none by default; list the endpoints that should carry it",
			objectstore.ErrInvalidConfig, contentEndpointsKey)
	}
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		switch name {
		case "":
			return fmt.Errorf("%w: %s holds an empty name at position %d",
				objectstore.ErrInvalidConfig, contentEndpointsKey, i)
		case allEndpoints:
			return fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete endpoint "+
				"names only: mounting the content route on every endpoint would put it on the public ones, "+
				"where the wrong layers guard it", objectstore.ErrInvalidConfig, contentEndpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every route on it twice", objectstore.ErrInvalidConfig, contentEndpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// schema declares this module's input items. The namespace is this module's own
// name, as it is for every registering module.
//
// Every item takes the primary source alone. The endpoint list decides where the
// content route is mounted, and the sweep interval sets a startup ticker, so
// both are what the process exposes and how it runs from the moment it starts: an
// environment variable or a command-line argument that could rewrite either
// would be a way to change that from outside the configuration file. The public
// URL is the deployment's external origin, which a running process cannot
// discover for itself, and it belongs with them. The upload window, the object
// lifetime and the URL lifetime are runtime tunables and are declared as settings
// instead.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			contentEndpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the content route is mounted on, by the names the " +
					"entry point's own configuration gives them; concrete names only, never *. Required of " +
					"a run that serves the content route — one with an entry point whose backend mints no " +
					"direct address — and unread by a run that does not",
			},
			publicURLItem: {
				Origins: config.OriginPrimary,
				Description: "the external origin a minted content address is prefixed with, such as " +
					"https://api.example.com, without a trailing slash; empty answers a root-relative address, " +
					"which is right only for a same-origin deployment",
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
