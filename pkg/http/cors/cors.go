// Package cors carries the CORS layer: it bridges gin-contrib/cors and registers one
// layer per listening endpoint this module's configuration names, so a browser is told
// which cross-origin requests an endpoint accepts.
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

// ModuleName is this module's name in the registry, the name a host writes to take it
// into a command, and the name its layer appears under on the assembled chain.
const ModuleName = "http.cors"

// ConfigNamespace is where this module's input items are mounted, and the complete path
// Decode is given.
const ConfigNamespace = ModuleName

// endpointsKey is the input item naming the listening endpoints this module guards,
// relative to the namespace; endpointsPath is its full path, which the failure for an
// empty set names so the reader knows which key to write.
const (
	endpointsKey  = "endpoints"
	endpointsPath = ConfigNamespace + "." + endpointsKey
)

// Config is the set of input items this module accepts: one CORS policy per listening
// endpoint, keyed by the endpoint name the entry point's configuration gives it.
type Config struct {
	// Endpoints holds one policy per listening endpoint, keyed by the
	// endpoint's name.
	Endpoints map[string]Policy `config:"endpoints"`
}

// Policy is one endpoint's CORS policy: the part of gin-contrib/cors's own
// configuration a host can state as values.
type Policy struct {
	// AllowAllOrigins allows every origin.
	AllowAllOrigins bool `config:"allow-all-origins"`
	// AllowOrigins lists the origins a cross-origin request may come from; the single entry "*" is read as AllowAllOrigins. An origin carries a scheme (http, https, or one the flags below add) or a wildcard, which the library refuses otherwise.
	AllowOrigins []string `config:"allow-origins"`
	// AllowMethods lists the methods a preflight may allow.
	AllowMethods []string `config:"allow-methods"`
	// AllowHeaders lists the request headers a preflight may allow.
	AllowHeaders []string `config:"allow-headers"`
	// ExposeHeaders lists the response headers a browser script may read.
	ExposeHeaders []string `config:"expose-headers"`
	// AllowCredentials lets a request carry credentials.
	AllowCredentials bool `config:"allow-credentials"`
	// MaxAge is how long a browser may cache the preflight result.
	MaxAge *time.Duration `config:"max-age"`
	// OptionsResponseStatusCode is the status a preflight is answered with.
	OptionsResponseStatusCode int `config:"options-response-status-code"`
	// AllowPrivateNetwork lets a public site reach a private-network address, through the
	// Access-Control-Allow-Private-Network response header.
	AllowPrivateNetwork bool `config:"allow-private-network"`
	// AllowWildcard lets an entry in AllowOrigins carry one "*", as in
	// https://*.example.com.
	AllowWildcard bool `config:"allow-wildcard"`
	// AllowBrowserExtensions adds the browser-extension schemas to the schemas an origin
	// may carry.
	AllowBrowserExtensions bool `config:"allow-browser-extensions"`
	// AllowWebSockets adds ws and wss to the schemas an origin may carry.
	AllowWebSockets bool `config:"allow-web-sockets"`
	// CustomSchemas adds further origin schemas.
	CustomSchemas []string `config:"custom-schemas"`

	// Unknown collects the keys of the entry that name none of the fields above.
	Unknown map[string]any `config:",remain"`
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &Config{}}},
		Items: map[string]config.Item{
			endpointsKey: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the CORS layer guards, each keyed by the " +
					"name the entry point's configuration gives it and carrying that endpoint's " +
					"CORS policy; at least one is required",
			},
		},
	}
}

func readConfig(reader config.Reader) (Config, error) {
	var cfg Config
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type endpointLayer struct {
	name    string
	handler gin.HandlerFunc
}

// validate refuses a configuration this module cannot serve: an empty endpoint set, or a policy the library would reject or panic on. Prepare calls it, so a bad policy ends the startup naming the endpoint and the key rather than with the library's own refusal.
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

// build returns the layer for each configured endpoint in endpoint-name order, so two runs register the layers in one order. It validates first: cors.New panics on a configuration Validate refuses, and a panic inside Init would take the process down.
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

func validStatusCode(code int) bool { return code >= 100 && code <= 599 }

func quoted(names []string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, strconv.Quote(name))
	}
	return strings.Join(out, ", ")
}

func plural(names []string) string {
	if len(names) == 1 {
		return "is"
	}
	return "are"
}
