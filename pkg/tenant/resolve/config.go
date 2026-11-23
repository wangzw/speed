package resolve

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// The keys this module accepts, relative to its namespace.
const (
	endpointsItem  = "endpoints"
	extractorItem  = "extractor"
	headerNameItem = "header-name"
	subdomainItem  = "subdomain-index"
	pathIndexItem  = "path-index"
)

// A configuration selects one of these built-in extractor names, and a module
// registering its own chooses a name that is not one of them.
const (
	// HeaderExtractor is the name the built-in header extractor is registered
	// under.
	HeaderExtractor = "header"
	// SubdomainExtractor is the name the built-in subdomain extractor is
	// registered under.
	SubdomainExtractor = "subdomain"
	// PathExtractor is the name the built-in path extractor is registered under.
	PathExtractor = "path"
)

// configDefaults is what this module is worth without a configuration file.
//
// There are no defaults for the endpoints or for the selected extractor.
// Guessing which endpoints are multi-tenant would be a guess about which
// traffic is scoped, and guessing how a name is carried would be a guess about
// the shape of the API's addresses; a module that cannot answer either fails
// the startup instead, naming the key it is missing.
var configDefaults = moduleConfig{
	HeaderName:     "X-Tenant",
	SubdomainIndex: 0,
	PathIndex:      0,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints this module guards. The names
	// are the entry point's. Its surface can list the endpoints it declares,
	// and this module does not take that list to choose what to guard: the
	// list is this module's own input rather than something followed from the
	// entry point's configuration, which would tie this module's
	// configuration shape to another module's.
	Endpoints []string `config:"endpoints"`
	// Extractor names the extractor this module resolves names with, out of
	// the ones registered. Exactly one is in force: two ways to read a name
	// off one request would mean two answers to which tenant it is, and any
	// precedence between them would be a way for a caller to choose.
	Extractor string `config:"extractor"`
	// HeaderName is the header the built-in header extractor reads.
	HeaderName string `config:"header-name"`
	// SubdomainIndex is which dot-separated label of the host the built-in
	// subdomain extractor reads, counting from the left and from zero. A
	// port in the host is not a label and is dropped before counting.
	SubdomainIndex int `config:"subdomain-index"`
	// PathIndex is which segment of the request path the built-in path
	// extractor reads, counting from the left and from zero, with leading
	// and trailing slashes not counting as segments.
	PathIndex int `config:"path-index"`
}

// settings is the configuration this module actually runs on.
type settings struct {
	endpoints []string
	extractor string
	header    string
	subdomain int
	path      int
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on.
func (c moduleConfig) resolve() (settings, error) {
	if c.SubdomainIndex < 0 {
		return settings{}, fmt.Errorf("%s: %s must not be negative, got %d",
			ConfigNamespace, subdomainItem, c.SubdomainIndex)
	}
	if c.PathIndex < 0 {
		return settings{}, fmt.Errorf("%s: %s must not be negative, got %d",
			ConfigNamespace, pathIndexItem, c.PathIndex)
	}
	// The endpoint list is a set. The entry point's surface takes an endpoint
	// by name and appends the layer to whatever is already there, so a name
	// written twice would register this module's layer on that endpoint a
	// second time: the roster lookup and the identifier's binding to the
	// logger would each run twice per request, and the second registration
	// buys nothing. That is a mistake to report rather than a repeat to drop
	// quietly.
	seen := make(map[string]struct{}, len(c.Endpoints))
	for _, name := range c.Endpoints {
		if _, twice := seen[name]; twice {
			return settings{}, fmt.Errorf("%s.%s names the endpoint %q twice, which would "+
				"register the tenant resolution layer on it twice and run the roster lookup "+
				"and the identifier binding twice for every request it carries: %w",
				ConfigNamespace, endpointsItem, name, tenant.ErrDuplicateEndpoint)
		}
		seen[name] = struct{}{}
	}
	return settings{
		endpoints: c.Endpoints,
		extractor: c.Extractor,
		header:    c.HeaderName,
		subdomain: c.SubdomainIndex,
		path:      c.PathIndex,
	}, nil
}

// schema declares this module's input items. The list is collected over every
// registered module whether or not it runs, so the namespace has to be this
// module's own name: two modules declaring the same path is a startup failure
// even when only one of them ever runs. That is also why a third-party
// extractor declares its own items under its own module's namespace rather
// than under this one.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints whose requests are resolved to a " +
					"tenant, by the names the entry point's own configuration gives them",
			},
			extractorItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "which registered extractor reads the tenant name off a " +
					"request: header, subdomain or path, or a name a module registered",
			},
			headerNameItem: {
				Description: "the header the header extractor reads the tenant name from",
			},
			subdomainItem: {
				Description: "which dot-separated label of the host the subdomain " +
					"extractor reads, counting from the left",
			},
			pathIndexItem: {
				Description: "which segment of the request path the path extractor " +
					"reads, counting from the left",
			},
		},
	}
}

// readSettings decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}

// prepare states this module's stance, or reports what it cannot start without.
//
// The two keys that have no default decide it, and each is named on its own: a
// deployment that wrote neither, and one that named endpoints but forgot how a
// name arrives, are looking at different lines to write. A command that took
// this module in has to start it, so an unwritten key is a startup failure
// rather than a stance to stand down from — the endpoints it was meant to
// guard would otherwise be served with no tenant, and every scoped statement
// refused, with nothing said beyond a diagnostic line.
func prepare(reader config.Reader) (core.Enablement, error) {
	set, err := readSettings(reader)
	if err != nil {
		return core.Enablement{}, err
	}
	if len(set.endpoints) == 0 {
		return core.Enablement{}, unwritten(endpointsItem)
	}
	if set.extractor == "" {
		return core.Enablement{}, unwritten(extractorItem)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// unwritten is the failure of an input item this module cannot start without.
//
// It names the key, and it names the other way out: a command that does not
// want tenant resolution leaves this module out of it, which is the only way
// left for a module not to run.
//
// A key the decoder found absent never reaches this: Decode raises
// config.ErrMissingRequired and names the path itself, and prepare carries
// that failure out as it stands.
func unwritten(key string) error {
	return fmt.Errorf("%s: nothing is resolved: %s.%s is unwritten, so requests reach "+
		"handlers with no tenant and every scoped statement is refused. Write that key "+
		"to run this module, or leave it out of the command",
		ConfigNamespace, ConfigNamespace, key)
}
