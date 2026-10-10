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

// ModuleName is this module's name in the registry and the token that opens
// every diagnostic line the module writes. A host writes it into the commands
// whose traffic should be limited.
//
// The capability directory and the module name are the same string here, so
// the diagnostics carry this one token: a format that wrote a prefix as well
// would render the name twice.
const ModuleName = "ratelimit"

// ConfigNamespace is where this module's input items are mounted, and where
// its settings are declared. It is this module's own name, the way a
// registering module's namespace always is.
const ConfigNamespace = ModuleName

// The two setting items, and the full paths they are declared and read under.
//
// The settings module registers every declared item under its full path — the
// namespace, a dot, then the item key — and looks a key up by that exact path,
// so a read names the full path and not the bare item key. The paths derive
// from ConfigNamespace and the item keys, and they have to stay in step with
// what settingsSchema registers: a read of a path nothing declares finds no
// declaration and fails open on every request.
const (
	windowKey = "window"
	limitKey  = "limit"

	windowPath = ConfigNamespace + "." + windowKey
	limitPath  = ConfigNamespace + "." + limitKey
)

// Limiter is the capability this module delivers. A layer writes it into its
// own After or Before to place itself inside or outside rate limiting.
//
// The method is unexported, so a caller that takes the instance up out of the
// registry can do nothing with it, and the identifier appears as
// (*Limiter)(nil), the token. The decision is made in the middleware and
// nowhere else. A type outside this package satisfies the interface by
// embedding it, which is how an instance the host supplies for this module
// delivers the capability.
type Limiter interface {
	limit()
}

// endpointsKey is the input item naming the endpoints to guard, and
// endpointsPath its full path, which the startup failure for an empty list
// names so the host knows which key to write.
const (
	endpointsKey  = "endpoints"
	endpointsPath = ConfigNamespace + "." + endpointsKey
)

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
// zero leaves the window index undefined. The window is bounded above as well,
// by the longest one whose double still fits a duration.
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

// windowInDomain and limitInDomain are the two settings' domains: the values
// this layer acts on.
//
// Each is the one statement of its domain, and it is judged in two places.
// The settings declaration carries it, so the settings store refuses a write
// outside it before the row is written; thresholds applies it to what a
// decision reads, since a row stored before the domain was declared, or
// written around the store, is served as it is, and the layer stands aside on
// such a value rather than act on it.
func windowInDomain(window time.Duration) bool { return window > 0 && window <= maxWindow }

func limitInDomain(limit int) bool { return limit > 0 }

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &Config{}}},
		Items: map[string]config.Item{
			endpointsKey: {
				Description: "the listening endpoints whose requests are limited, by the names " +
					"the entry point's configuration gives them; at least one is required",
			},
		},
	}
}

// settingsSchema declares this module's runtime parameters, each with the
// domain this layer acts on.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			windowKey: {
				Description: "the length of one counting window, which must be positive " +
					"and short enough for two of them to fit in a duration",
				Domain: settings.DomainOf("the positive durations up to "+maxWindow.String()+
					", the longest whose double a duration holds", windowInDomain),
			},
			limitKey: {
				Description: "the requests one identity string may make in one window " +
					"within one tenant, which must be positive",
				Domain: settings.DomainOf("the positive integers", limitInDomain),
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
