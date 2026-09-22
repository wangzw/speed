// Package ratelimit delivers request rate limiting per identity string.
//
// One middleware layer is registered on each listening endpoint this module's
// configuration names. A request that carries an identity string is counted
// once, and a request whose estimate exceeds the limit for the current window
// is answered 429 with a Retry-After header. The counting goes through the
// key-value primitives, so the module holds no state of its own across
// replicas.
package ratelimit

import (
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose traffic should be limited.
const ModuleName = "ratelimit"

// ConfigNamespace is where this module's input items are mounted, and where
// its settings are declared. It is this module's own name, the way a
// registering module's namespace always is.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the process's
// standard error. The reporting chain itself may be absent or switched off, so
// these lines do not go through it.
const diagnosticPrefix = "ratelimit: "

// The two setting items, relative to the namespace.
const (
	windowKey = "window"
	limitKey  = "limit"
)

// Limiter is the capability this module delivers. A layer writes it into its
// own After or Before to place itself inside or outside rate limiting.
//
// The method is unexported, so no type outside this package satisfies the
// interface: the identifier only ever appears as (*Limiter)(nil), and a caller
// that takes the instance up out of the registry can do nothing with it. The
// decision is made in the middleware and nowhere else.
type Limiter interface {
	limit()
}

// Config is the set of input items this module accepts.
//
// There is no default: an empty list means this module has no endpoint to
// guard, which is a startup failure rather than a module that quietly does
// nothing.
type Config struct {
	// Endpoints names the listening endpoints the limiting layer is
	// registered on.
	Endpoints []string `config:"endpoints"`
}

// options carries the two settings items and their declared defaults.
//
// A host that writes no row for either key gets these values. Both domains
// exclude zero: a limit of zero rejects every request that carries an identity
// string, because the increment happens before the decision, and a window of
// zero leaves the window index undefined.
type options struct {
	Window time.Duration `config:"window"`
	Limit  int           `config:"limit"`
}

// defaults is the prototype the settings module reads the declared defaults
// off. It never receives a value.
var defaults = options{
	Window: time.Minute,
	Limit:  100,
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &Config{}}},
		Items: map[string]config.Item{
			"endpoints": {
				Description: "the listening endpoints whose requests are limited, " +
					"or empty to guard none of them",
			},
		},
	}
}

// settingsSchema declares this module's runtime parameters.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			windowKey: {Description: "the length of one counting window, which must be positive"},
			limitKey: {
				Description: "the requests one identity string may make in one window " +
					"within one tenant, which must be positive",
			},
		},
	}
}

// readConfig decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
func readConfig(reader config.Reader) (Config, error) {
	var cfg Config
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// duplicateEndpoint reports the first endpoint name that appears more than
// once, if any.
//
// A repeated name registers the same layer twice, and two registrations are
// two layers: the endpoint then decides twice per request and the effective
// limit is halved. The chain listing shows two layers, but not that they come
// from one mistake.
func duplicateEndpoint(names []string) (string, bool) {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			return name, true
		}
		seen[name] = struct{}{}
	}
	return "", false
}
