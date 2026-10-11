package admission

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// The configuration key this module accepts, relative to its namespace.
const endpointsItem = "endpoints"

// The settings this module declares, relative to its namespace, and the full
// paths a read goes by.
const (
	requireMFAItem = "require-mfa"
	requireSSOItem = "require-sso"
	checkTTLItem   = "check-ttl"

	requireMFAKey = ConfigNamespace + "." + requireMFAItem
	requireSSOKey = ConfigNamespace + "." + requireSSOItem
	checkTTLKey   = ConfigNamespace + "." + checkTTLItem
)

// maxCheckTTL is the longest a membership or a permission may be remembered. It
// is the bound of how long a removed member is still admitted by a replica that
// has not heard of the removal, so it is a ceiling and not a default.
const maxCheckTTL = 60 * time.Second

// allEndpoints is the name that stands for every endpoint in the endpoint lists
// of the gateway and of the modules that took this form from it. This list
// refuses it.
const allEndpoints = "*"

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints. Guarding every endpoint would cover the
// endpoint the authentication routes are mounted on, where an impersonating
// identity has no tenant and the layer would answer 404 to the request that
// ends the impersonation; and guessing which endpoints carry tenant traffic
// would be a guess about which traffic is checked. A module that cannot answer
// fails the startup, naming the key.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints this module guards, by the
	// entry point's own names. The list is a set of concrete names: the name
	// that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

// options carries the settings items and their declared defaults. The two
// switches are read per tenant and fall back to the global row and then to the
// default declared here, which is off; the lifetime is read from the global
// layer alone.
type options struct {
	RequireMFA bool          `config:"require-mfa"`
	RequireSSO bool          `config:"require-sso"`
	CheckTTL   time.Duration `config:"check-ttl"`
}

// defaults is the prototype the settings module reads the declared defaults
// off. It never receives a value.
var defaults = options{CheckTTL: 5 * time.Second}

// checkTTLInDomain is the one statement of the lifetime's range: the settings
// store holds a write to it, and a read is judged against it again, since a
// value stored before the domain was declared, or written around the store, is
// served as it is.
func checkTTLInDomain(d time.Duration) bool { return d >= 0 && d <= maxCheckTTL }

// resolved is the configuration this module actually runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on.
func (c moduleConfig) resolve() (resolved, error) {
	path := ConfigNamespace + "." + endpointsItem
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The layer guards the endpoints it "+
			"is told to and none by default; list the endpoints that carry tenant traffic, or "+
			"leave this module out of the command", authn.ErrInvalidConfig, path)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, path, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This layer takes "+
				"concrete endpoint names only: guarding every endpoint would cover the one the "+
				"authentication routes are mounted on, where a request that ends an "+
				"impersonation has no tenant and would be answered 404",
				authn.ErrInvalidConfig, path, allEndpoints, i)
		}
		// The list is a set. The entry point appends a layer to whatever is
		// already on an endpoint, so a name written twice would run the
		// membership check twice for every request it carries.
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%s names the endpoint %q twice, which would register "+
				"the admission layer on it twice and run the membership check twice for every "+
				"request it carries: %w", path, name, tenant.ErrDuplicateEndpoint)
		}
		seen[name] = struct{}{}
	}
	return resolved{endpoints: c.Endpoints}, nil
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints whose requests are checked for membership " +
					"of the tenant they resolved to, by the names the entry point's own " +
					"configuration gives them; concrete names only, never *",
			},
		},
	}
}

// settingsSchema declares this module's runtime parameters, each with the
// domain the code acts on.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			requireMFAItem: {
				Description: "whether a tenant's members must have authenticated with more " +
					"than one kind of factor to make a request in that tenant; per tenant",
			},
			requireSSOItem: {
				Description: "whether a tenant's members must have signed in through one of " +
					"that tenant's own identity providers to make a request in that tenant; " +
					"per tenant",
			},
			checkTTLItem: {
				Description: "how long a membership and a permission read from the account " +
					"module stay in the process, from 0, which switches the memory off, to " +
					maxCheckTTL.String() + "; global",
				Domain: settings.DomainOf("durations from 0 to "+maxCheckTTL.String(), checkTTLInDomain),
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

// prepare states this module's stance, or reports what it cannot start without.
// A command that took the module in has to start it, so an unwritten key is a
// startup failure and not a stance to stand down from: the endpoints it was meant
// to guard would be served with no membership check at all. A key the decoder
// found absent never reaches resolve: Decode raises config.ErrMissingRequired and
// names the path itself, and prepare carries that failure out as it stands.
func prepare(reader config.Reader) (core.Enablement, error) {
	if _, err := readConfig(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}
