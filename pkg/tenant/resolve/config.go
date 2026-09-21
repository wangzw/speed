package resolve

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The keys this module accepts, relative to its namespace.
const (
	endpointsItem  = "endpoints"
	extractorItem  = "extractor"
	headerNameItem = "header-name"
	subdomainItem  = "subdomain-index"
	pathIndexItem  = "path-index"
)

// The names the built-in extractors are registered under. A configuration
// selects one of them by name, and a module registering its own chooses a name
// that is not one of these.
const (
	HeaderExtractor    = "header"
	SubdomainExtractor = "subdomain"
	PathExtractor      = "path"
)

// configDefaults is what this module is worth without a configuration file.
//
// There are no defaults for the endpoints or for the selected extractor.
// Guessing which endpoints are multi-tenant would be a guess about which
// traffic is scoped, and guessing how a name is carried would be a guess about
// the shape of the API's addresses; a module that cannot answer either states
// itself disabled instead, with the reason naming the key.
var configDefaults = moduleConfig{
	HeaderName:     "X-Tenant",
	SubdomainIndex: 0,
	PathIndex:      0,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints this module guards. The names
	// are the entry point's, and there is no way to ask it for the set: its
	// surface takes an endpoint by name and enumerates none. So the list is
	// this module's own input rather than something followed from the entry
	// point's configuration, which would tie this module's configuration
	// shape to another module's.
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

// prepare states whether this module runs.
//
// The two keys that have no default decide it, and they are told apart in the
// reason: a deployment that named neither, and one that named endpoints but
// forgot how a name arrives, are looking at different lines to write.
func prepare(reader config.Reader) (core.Enablement, error) {
	set, err := readSettings(reader)
	if err != nil {
		if errors.Is(err, config.ErrMissingRequired) {
			return core.Enablement{State: core.StateDisabled, Reason: missingReason(err)}, nil
		}
		return core.Enablement{}, err
	}
	if len(set.endpoints) == 0 {
		return core.Enablement{State: core.StateDisabled, Reason: standDownReason(endpointsItem)}, nil
	}
	if set.extractor == "" {
		return core.Enablement{State: core.StateDisabled, Reason: standDownReason(extractorItem)}, nil
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// standDownReason says which key is unwritten, and what leaving it unwritten
// would mean. The startup account is the only place an operator learns this:
// a module that stood down runs no part of itself, and the endpoints it was
// meant to guard go on being served without a tenant.
func standDownReason(key string) string {
	return fmt.Sprintf(
		"nothing is resolved: %s.%s is unwritten, so requests reach handlers with no "+
			"tenant and every scoped statement is refused. Write that key to switch "+
			"tenant resolution on", ConfigNamespace, key)
}

// missingReason names the key the configuration module reported as required and
// absent. It is a stand-down rather than a startup failure — the same fact as
// an empty list, reached by a different route — so the sentence is the same
// one, with the key taken from the decoder rather than assumed.
func missingReason(err error) string {
	return fmt.Sprintf(
		"nothing is resolved: %s. Write the keys %s.%s and %s.%s to switch tenant "+
			"resolution on", err, ConfigNamespace, endpointsItem,
		ConfigNamespace, extractorItem)
}
