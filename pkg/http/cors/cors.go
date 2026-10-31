// Package cors carries the CORS layer: it bridges gin-contrib/cors and
// registers one layer per listening endpoint this module's configuration names,
// so a browser is told which cross-origin requests an endpoint accepts.
//
// It is a package of its own rather than part of the entry point, because the
// bridge adds a third-party dependency and the entry point's import budget
// (pkg/http/deps_test.go) admits nothing beyond gin's own closure. It is a
// module of its own: a host takes it into a command by name, and it registers
// through the entry point's Middleware surface like any other layer.
//
// The layer belongs to the guard stage and asks for the outside of it with
// OrderOutermost. The stage is the framework's guarantee; the outside is a
// preference, because another layer taking the same sentinel ties with this one
// and registration order settles the tie with nothing reported. The position is
// not a style choice: a CORS layer inside a layer that refuses a request leaves
// the refusal without the CORS headers, so a browser
// reports a network error instead of the status it was sent; and a preflight
// inside an identifying layer is refused before it is answered, so the browser
// never sends the request the preflight was for. At the outside of the guard
// stage, a refusal from any layer inside it — a 429 from an address limit, a 401
// from the gateway — carries the CORS headers, and the preflight is answered by
// this layer before it reaches the layers that would refuse it.
package cors

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
)

// ModuleName is this module's name in the registry, the name a host writes to
// take it into a command, and the name its layer appears under on the assembled
// chain.
const ModuleName = "http.cors"

// ConfigNamespace is where this module's input items are mounted, and the
// complete path Decode is given. A registering module's namespace is its own
// name.
const ConfigNamespace = ModuleName

// endpointsKey is the input item naming the listening endpoints this module
// guards, relative to the namespace; endpointsPath is its full path, which the
// failure for an empty set names so the reader knows which key to write.
const (
	endpointsKey  = "endpoints"
	endpointsPath = ConfigNamespace + "." + endpointsKey
)

// Config is the set of input items this module accepts: one CORS policy per
// listening endpoint, keyed by the endpoint name the entry point's
// configuration gives it.
//
// The policy is per endpoint rather than one setting for the module, because a
// host that serves a public API and a management API on two endpoints gives them
// different origins. An endpoint absent from the map carries no CORS layer: the
// module registers one layer per entry and nothing on the others, which is how a
// host switches the layer off for one endpoint while keeping it on for another.
type Config struct {
	Endpoints map[string]Policy `config:"endpoints"`
}

// Policy is one endpoint's CORS policy: the part of gin-contrib/cors's own
// configuration a host can state as values. The two fields that take functions
// are left out, because a function is not something a configuration file can
// carry.
//
// An item the policy leaves empty or nil takes the library's own default rather
// than a second default kept here; libraryConfig starts from the library's
// defaults and overlays only the fields the policy sets.
type Policy struct {
	// AllowAllOrigins allows every origin. It is mutually exclusive with a
	// non-empty AllowOrigins: the library refuses a policy that gives both,
	// because "every origin" and "this list of origins" are two answers to one
	// question.
	AllowAllOrigins bool `config:"allow-all-origins"`
	// AllowOrigins lists the origins a cross-origin request may come from. The
	// single entry "*" is read as AllowAllOrigins. An origin carries a scheme —
	// http, https, or one of the schemas the flags below add — or a wildcard,
	// which the library refuses otherwise.
	AllowOrigins []string `config:"allow-origins"`
	// AllowMethods lists the methods a preflight may allow. An empty list takes
	// the library's default set.
	AllowMethods []string `config:"allow-methods"`
	// AllowHeaders lists the request headers a preflight may allow. An empty
	// list takes the library's default set.
	AllowHeaders []string `config:"allow-headers"`
	// ExposeHeaders lists the response headers a browser script may read.
	ExposeHeaders []string `config:"expose-headers"`
	// AllowCredentials lets a request carry credentials. A browser refuses a
	// wildcard origin beside credentials, so combining it with AllowAllOrigins
	// answers a request the browser then rejects.
	AllowCredentials bool `config:"allow-credentials"`
	// MaxAge is how long a browser may cache the preflight result. Absent takes
	// the library's default; an explicit 0 sends no Max-Age and lets the browser
	// cache by its own rule.
	MaxAge *time.Duration `config:"max-age"`
	// OptionsResponseStatusCode is the status a preflight is answered with. 0
	// takes the library's default, 204.
	OptionsResponseStatusCode int `config:"options-response-status-code"`
	// AllowPrivateNetwork lets a public site reach a private-network address,
	// through the Access-Control-Allow-Private-Network response header.
	AllowPrivateNetwork bool `config:"allow-private-network"`
	// AllowWildcard lets an entry in AllowOrigins carry one "*", as in
	// https://*.example.com.
	AllowWildcard bool `config:"allow-wildcard"`
	// AllowBrowserExtensions adds the browser-extension schemas to the schemas
	// an origin may carry.
	AllowBrowserExtensions bool `config:"allow-browser-extensions"`
	// AllowWebSockets adds ws and wss to the schemas an origin may carry.
	AllowWebSockets bool `config:"allow-web-sockets"`
	// CustomSchemas adds further origin schemas.
	CustomSchemas []string `config:"custom-schemas"`

	// Unknown collects the keys of the entry that name none of the fields above.
	// Configuration checks no key inside a container leaf, so without it a
	// misspelled allow-origin would vanish and the library's default would
	// govern an endpoint its operator believed configured otherwise; the entry
	// is refused instead. The library's own AllowFiles is not among the fields:
	// a file origin is expressible through CustomSchemas, so omitting it costs a
	// host nothing and keeps the dangerous switch off one list.
	Unknown map[string]any `config:",remain"`
}

// schema declares this module's input items.
//
// endpoints takes the environment as well as the configuration file, as JSON
// that replaces the whole table. The origins a deployment is served from differ
// between runs of one binary, and a run with no configuration file still has to
// name the endpoints the layer guards. The value is a map of policies, so the
// command line has no form for it.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &Config{}}},
		Items: map[string]config.Item{
			endpointsKey: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "the listening endpoints the CORS layer guards, each keyed by the " +
					"name the entry point's configuration gives it and carrying that endpoint's " +
					"CORS policy; at least one is required",
			},
		},
	}
}

// readConfig decodes this module's section. Prepare and New both go through it,
// so the two see one configuration.
func readConfig(reader config.Reader) (Config, error) {
	var cfg Config
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// endpointLayer ties one configured endpoint's name to the layer built for it.
type endpointLayer struct {
	name    string
	handler gin.HandlerFunc
}

// validate refuses a configuration this module cannot serve: an empty endpoint
// set, or a policy the library would reject or panic on. Prepare calls it, so a
// bad policy ends the startup with a message naming the endpoint and the key
// rather than with the library's own refusal.
func (c Config) validate() error {
	if len(c.Endpoints) == 0 {
		return fmt.Errorf("%s: no listening endpoint is guarded: %s is empty. List the endpoints "+
			"the CORS layer is registered on, each keyed by the name the entry point's configuration "+
			"gives it: %w", ModuleName, endpointsPath, config.ErrMissingRequired)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Endpoints)) {
		if err := c.Endpoints[name].validate(); err != nil {
			return fmt.Errorf("%s: endpoint %q: %w", ModuleName, name, err)
		}
	}
	return nil
}

// build returns the layer for each configured endpoint, in endpoint-name order
// so two runs over one configuration register the layers in one order. It
// validates first: cors.New panics on a configuration Validate refuses, and a
// panic inside Init would take the process down with a stack trace during a
// stage whose failures are meant to end the startup in an orderly way.
func (c Config) build() ([]endpointLayer, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(c.Endpoints))
	layers := make([]endpointLayer, 0, len(names))
	for _, name := range names {
		layers = append(layers, endpointLayer{
			name:    name,
			handler: cors.New(c.Endpoints[name].libraryConfig()),
		})
	}
	return layers, nil
}

// validate refuses a policy the library would reject or panic on, before the
// library ever sees it.
//
// The library's own Validate covers the origin rules; the checks before it cover
// the three things Validate does not, each of which the library would otherwise
// act on silently or fatally: a key no field takes, a MaxAge or a status the
// response cannot carry, and an origin with more than one "*", on which the
// wildcard parsing panics outright.
func (p Policy) validate() error {
	if len(p.Unknown) > 0 {
		keys := slices.Sorted(maps.Keys(p.Unknown))
		return fmt.Errorf("%w: %s %s not among the keys a policy takes. Correct the spelling, "+
			"or drop the entry", ErrInvalidPolicy, quoted(keys), plural(keys))
	}
	if p.MaxAge != nil && *p.MaxAge < 0 {
		return fmt.Errorf("%w: max-age is %s; 0 sends no Max-Age and a positive duration sets "+
			"one", ErrInvalidPolicy, *p.MaxAge)
	}
	if p.OptionsResponseStatusCode != 0 && !validStatusCode(p.OptionsResponseStatusCode) {
		return fmt.Errorf("%w: options-response-status-code is %d, which is not a status an HTTP "+
			"response can carry", ErrInvalidPolicy, p.OptionsResponseStatusCode)
	}
	if p.AllowWildcard {
		for _, origin := range p.AllowOrigins {
			if strings.Count(origin, "*") > 1 {
				return fmt.Errorf("%w: allow-wildcard is on and the origin %q carries more than one "+
					"\"*\", which the library's wildcard parsing cannot place. Use one wildcard per "+
					"origin", ErrInvalidPolicy, origin)
			}
		}
	}
	if err := p.libraryConfig().Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
	}
	return nil
}

// libraryConfig maps the policy onto the library's configuration, starting from
// the library's own defaults so that an item the policy leaves empty or nil takes
// the library's default instead of a second set of defaults kept here. AllowMethods,
// AllowHeaders and MaxAge are the three the library has a default for; every
// other field below carries the policy's value as it stands.
func (p Policy) libraryConfig() cors.Config {
	c := cors.DefaultConfig()
	c.AllowAllOrigins = p.AllowAllOrigins
	c.AllowOrigins = p.AllowOrigins
	if len(p.AllowMethods) > 0 {
		c.AllowMethods = p.AllowMethods
	}
	if len(p.AllowHeaders) > 0 {
		c.AllowHeaders = p.AllowHeaders
	}
	c.ExposeHeaders = p.ExposeHeaders
	c.AllowCredentials = p.AllowCredentials
	if p.MaxAge != nil {
		c.MaxAge = *p.MaxAge
	}
	c.OptionsResponseStatusCode = p.OptionsResponseStatusCode
	c.AllowPrivateNetwork = p.AllowPrivateNetwork
	c.AllowWildcard = p.AllowWildcard
	c.AllowBrowserExtensions = p.AllowBrowserExtensions
	c.AllowWebSockets = p.AllowWebSockets
	c.CustomSchemas = p.CustomSchemas
	return c
}

// validStatusCode reports whether a status a policy sets is one an HTTP response
// can carry.
func validStatusCode(code int) bool { return code >= 100 && code <= 599 }

// quoted renders names for a message, each quoted.
func quoted(names []string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, strconv.Quote(name))
	}
	return strings.Join(out, ", ")
}

// plural is the verb agreeing with a count of keys.
func plural(names []string) string {
	if len(names) == 1 {
		return "is"
	}
	return "are"
}
