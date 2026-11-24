package resolve

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

const (
	endpointsItem  = "endpoints"
	extractorItem  = "extractor"
	headerNameItem = "header-name"
	subdomainItem  = "subdomain-index"
	pathIndexItem  = "path-index"
)

// The names the built-in extractors are registered under.
const (
	HeaderExtractor    = "header"
	SubdomainExtractor = "subdomain"
	PathExtractor      = "path"
)

var configDefaults = moduleConfig{
	HeaderName:     "X-Tenant",
	SubdomainIndex: 0,
	PathIndex:      0,
}

type moduleConfig struct {
	Endpoints      []string `config:"endpoints"`
	Extractor      string   `config:"extractor"`
	HeaderName     string   `config:"header-name"`
	SubdomainIndex int      `config:"subdomain-index"`
	PathIndex      int      `config:"path-index"`
}

type settings struct {
	endpoints []string
	extractor string
	header    string
	subdomain int
	path      int
}

func (c moduleConfig) resolve() (settings, error) {
	if c.SubdomainIndex < 0 {
		return settings{}, fmt.Errorf("%s: %s must not be negative, got %d",
			ConfigNamespace, subdomainItem, c.SubdomainIndex)
	}
	if c.PathIndex < 0 {
		return settings{}, fmt.Errorf("%s: %s must not be negative, got %d",
			ConfigNamespace, pathIndexItem, c.PathIndex)
	}
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

func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}

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

func unwritten(key string) error {
	return fmt.Errorf("%s: nothing is resolved: %s.%s is unwritten, so requests reach "+
		"handlers with no tenant and every scoped statement is refused. Write that key "+
		"to run this module, or leave it out of the command",
		ConfigNamespace, ConfigNamespace, key)
}
