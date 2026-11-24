package gate

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace.
const (
	endpointsItem      = "endpoints"
	budgetItem         = "call-budget"
	inFlightItem       = "in-flight"
	mountEndpointsItem = "mount.endpoints"
	mountBasesItem     = "mount.bases"
	mountPublicURLItem = "mount.public-url"
	trustedOriginsItem = "trusted-origins"
)

// allEndpoints is the reserved endpoint name that stands for every endpoint the
// entry point declares. The entry point refuses it as the name of an endpoint,
// so a list can write it without colliding with one.
const allEndpoints = "*"

// configDefaults is what this module is worth without a configuration file.
//
// Without a file the gateway guards every endpoint the entry point declares:
// the side a mistake falls on is refusal, so an endpoint nobody thought of is
// guarded and not served open. Narrowing the list, or registering an exemption,
// is how a route or an endpoint is given a way past it.
var configDefaults = moduleConfig{
	Endpoints:  []string{allEndpoints},
	CallBudget: 5 * time.Second,
	InFlight:   64,
	Mount: mountConfig{Bases: bases{
		Default: mount.DefaultBasePath,
		Tenants: mount.DefaultTenantsBasePath,
		System:  mount.DefaultSystemBasePath,
	}},
}

type moduleConfig struct {
	// Endpoints names the listening endpoints this gateway guards, by the
	// entry point's own names. The reserved name "*" stands for every declared
	// endpoint; a list of names narrows the set to those. An empty list is a
	// mistake and not a way to switch the gateway off: a gateway that guards
	// nothing would serve every route without a credential, and the way to have
	// no gateway is to leave this module out of the command.
	Endpoints []string `config:"endpoints"`
	// CallBudget is the deadline one call into a backend is given and the point
	// past which its answer is no longer honoured: a backend that answers late
	// ends the request as an outage, whether what it answered was a pass or a
	// refusal.
	//
	// It does not cut the wait short. The call is synchronous, so a backend
	// that ignores the deadline holds its goroutine and its in-flight slot
	// until it returns; abandoning it would either keep the slot, which
	// changes nothing, or release it and let hung goroutines grow without
	// bound. What bounds this module's own side is the gate below.
	CallBudget time.Duration `config:"call-budget"`
	// InFlight bounds how many calls into backends run at once. Past it a
	// request ends as an outage without waiting, so that a backend that stopped
	// answering cannot take every goroutine the server has.
	InFlight int `config:"in-flight"`
	// Mount is where the capability's own routes are mounted. The gateway
	// declares the keys and reads only what its own checks need; the modules
	// that mount routes read the same keys in their own Prepare.
	Mount mountConfig `config:"mount"`
	// TrustedOrigins are origins the cross-origin check and the return-to
	// check trust besides the origin of the public URL, each scheme://host[:port].
	// An entry is turned into the form a browser sends before it is compared:
	// lower-case, with the scheme's default port left out.
	TrustedOrigins []string `config:"trusted-origins"`
}

type mountConfig struct {
	// Endpoints is the endpoint each scope's routes are mounted on. The
	// capability's routes span policy domains, and the endpoint decides which
	// layers a request meets before its handler: the tenant-administration
	// routes are decided in the tenant's domain and need the tenant layers,
	// while the ordinary and platform-management routes are decided outside
	// it.
	Endpoints endpoints `config:"endpoints"`
	// Bases is the base path of each scope the capability's routes are mounted
	// under: the levels are published under different path segments.
	Bases bases `config:"bases"`
	// PublicURL is the origin the deployment is reached at from outside, which
	// the entry point cannot derive from a request.
	PublicURL string `config:"public-url"`
}

type endpoints struct {
	// Default is the endpoint the ordinary routes are mounted on. Empty means
	// the one endpoint the entry point declares.
	Default string `config:"default"`
	// Tenants is the endpoint the tenant-administration routes are mounted on.
	// It has no fallback: those routes are decided in the tenant policy domain,
	// and the endpoint has to be one both tenant layers cover. Empty leaves the
	// scope unmountable, which is the state of an assembly that publishes no
	// tenant-administration routes.
	Tenants string `config:"tenants"`
	// System is the endpoint the platform-management routes are mounted on.
	// Empty means the default endpoint: both are decided outside the tenant
	// policy domain and share a chain unless the deployment separates them.
	System string `config:"system"`
}

type bases struct {
	Default string `config:"default"`
	Tenants string `config:"tenants"`
	System  string `config:"system"`
}

type settings struct {
	// endpoints is the list as written, which may be the single reserved name.
	endpoints      []string
	budget         time.Duration
	inFlight       int
	mount          mountConfig
	trustedOrigins []string
}

func (s settings) allGuarded() bool {
	return len(s.endpoints) == 1 && s.endpoints[0] == allEndpoints
}

func (c moduleConfig) resolve() (settings, error) {
	s := settings{
		endpoints:      c.Endpoints,
		budget:         c.CallBudget,
		inFlight:       c.InFlight,
		mount:          c.Mount,
		trustedOrigins: c.TrustedOrigins,
	}
	if err := checkEndpoints(c.Endpoints); err != nil {
		return settings{}, err
	}
	if s.budget <= 0 {
		return settings{}, fmt.Errorf("%w: %s.%s is %s, and a call with no bound is one a hung "+
			"backend never gives back", authn.ErrInvalidConfig, ConfigNamespace, budgetItem, s.budget)
	}
	if s.inFlight <= 0 {
		return settings{}, fmt.Errorf("%w: %s.%s is %d, and a gateway that admits no call at all "+
			"refuses every request it is given", authn.ErrInvalidConfig, ConfigNamespace, inFlightItem, s.inFlight)
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{"default", s.mount.Bases.Default},
		{"tenants", s.mount.Bases.Tenants},
		{"system", s.mount.Bases.System},
	} {
		if err := checkBasePath(item.name, item.value); err != nil {
			return settings{}, err
		}
	}
	if err := checkPublicURL(s.mount.PublicURL); err != nil {
		return settings{}, err
	}
	// The cross-origin check is built from this same list when the module is
	// constructed, so the list is held to the rule that builds it: a wildcard,
	// user information, an empty port and a host that is not ASCII fail here and
	// not at the first request. The refusal is not given the value, which may
	// carry a password written where none belongs; the entry is named by its
	// position.
	if _, err := mount.TrustedOrigins(s.mount.PublicURL, s.trustedOrigins); err != nil {
		return settings{}, fmt.Errorf("%w: %s.%s or %s.%s is not an origin the cross-origin check "+
			"can trust: %v", authn.ErrInvalidConfig, ConfigNamespace, mountPublicURLItem,
			ConfigNamespace, trustedOriginsItem, err)
	}
	return s, nil
}

func checkEndpoints(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("%w: %s.%s is an empty list, which guards no endpoint and would serve "+
			"every route without a credential. Write %q to guard every endpoint, name the "+
			"endpoints to guard, or leave this module out of the command",
			authn.ErrInvalidConfig, ConfigNamespace, endpointsItem, allEndpoints)
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("%w: %s.%s holds an empty endpoint name, which names no endpoint the "+
				"entry point has", authn.ErrInvalidConfig, ConfigNamespace, endpointsItem)
		}
		// A name written twice would register the layer twice on that
		// endpoint: every request would be authenticated twice, every backend
		// asked twice, and every conclusion counted twice. The second
		// registration buys nothing, so this is a mistake to report rather
		// than a duplicate to drop quietly.
		if _, twice := seen[name]; twice {
			return fmt.Errorf("%w: %s.%s names the endpoint %q twice, which would register the "+
				"authentication layer on it twice and ask every backend twice per request",
				authn.ErrInvalidConfig, ConfigNamespace, endpointsItem, name)
		}
		seen[name] = struct{}{}
	}
	if _, wildcard := seen[allEndpoints]; wildcard && len(names) > 1 {
		return fmt.Errorf("%w: %s.%s writes %q beside named endpoints. The reserved name already "+
			"stands for every endpoint, so write it alone, or drop it and name the endpoints "+
			"to guard", authn.ErrInvalidConfig, ConfigNamespace, endpointsItem, allEndpoints)
	}
	return nil
}

func checkBasePath(item, p string) error {
	switch {
	case !strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: %s.%s.%s is %q, which does not begin with a slash",
			authn.ErrInvalidConfig, ConfigNamespace, mountBasesItem, item, clip(p))
	case strings.HasSuffix(p, "/"):
		return fmt.Errorf("%w: %s.%s.%s is %q, which ends with a slash or is the root. Write the "+
			"path without a trailing slash, such as /api/v1/auth", authn.ErrInvalidConfig, ConfigNamespace,
			mountBasesItem, item, clip(p))
	case strings.ContainsAny(p, "*:?#{}"):
		return fmt.Errorf("%w: %s.%s.%s is %q, which carries a wildcard, a parameter, a query or a "+
			"fragment, and routes are mounted under a literal path", authn.ErrInvalidConfig,
			ConfigNamespace, mountBasesItem, item, clip(p))
	case path.Clean(p) != p:
		return fmt.Errorf("%w: %s.%s.%s is %q, which is not in the cleaned form gin keeps a path in",
			authn.ErrInvalidConfig, ConfigNamespace, mountBasesItem, item, clip(p))
	}
	return nil
}

// checkPublicURL holds the public base to an origin, and to https unless the host
// is a loopback one. Traffic to a loopback host never leaves the machine, so the
// cookies the authn modules write there, which carry neither a prefix nor Secure,
// cross no network; on any other host http would carry them in the clear. Empty
// is allowed here: the modules that need the address refuse it themselves.
//
// A URL that does not parse is not quoted, and one that does is quoted without
// its user information, query or fragment: the value may carry a password written
// where no password belongs, and the refusal must not copy it into an error text.
func checkPublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("%w: %s.%s is not a URL with a scheme and a host",
			authn.ErrInvalidConfig, ConfigNamespace, mountPublicURLItem)
	}
	shown := clip(withoutUser(u))
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %s.%s is %q, and its scheme is neither https nor http",
			authn.ErrInvalidConfig, ConfigNamespace, mountPublicURLItem, shown)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.ForceQuery {
		return fmt.Errorf("%w: %s.%s is %q, which carries more than an origin: user "+
			"information, a path, a query or a fragment (user information, query and fragment are not "+
			"shown). Write scheme, host and port alone: the path is %s.%s", authn.ErrInvalidConfig, ConfigNamespace,
			mountPublicURLItem, shown, ConfigNamespace, mountBasesItem)
	}
	if u.Scheme == "http" && !cookiename.LoopbackHost(u.Hostname()) {
		return fmt.Errorf("%w: %s.%s is %q, which is not https. Cookies and redirects built on it "+
			"would cross the network in the clear: only a loopback host, whose traffic never leaves "+
			"the machine, is accepted over http. Use https",
			authn.ErrInsecureConfig, ConfigNamespace, mountPublicURLItem, shown)
	}
	return nil
}

func withoutUser(u *url.URL) string {
	bare := *u
	bare.User = nil
	bare.RawQuery = ""
	bare.ForceQuery = false
	bare.Fragment = ""
	bare.RawFragment = ""
	return bare.String()
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints this gateway guards, by the names the " +
					"entry point's own configuration gives them; * stands for every endpoint " +
					"and is the default",
			},
			budgetItem: {
				Description: "how long one call into a credential-verifying backend may take " +
					"before the request ends as an outage",
			},
			inFlightItem: {
				Description: "how many calls into credential-verifying backends may run at " +
					"once before further requests end as an outage",
			},
			mountEndpointsItem + ".default": {
				Description: "the endpoint the ordinary authentication routes are mounted " +
					"on; empty takes the entry point's only endpoint",
			},
			mountEndpointsItem + ".tenants": {
				Description: "the endpoint the tenant-administration routes are mounted on; " +
					"it has to be one the tenant resolution and admission layers cover, " +
					"and empty leaves that scope unmountable",
			},
			mountEndpointsItem + ".system": {
				Description: "the endpoint the platform-administration routes are mounted " +
					"on; empty takes the default endpoint",
			},
			mountBasesItem + ".default": {
				Description: "the base path the ordinary authentication routes are mounted under",
			},
			mountBasesItem + ".tenants": {
				Description: "the base path the tenant-administration routes are mounted under",
			},
			mountBasesItem + ".system": {
				Description: "the base path the platform-administration routes are mounted under",
			},
			mountPublicURLItem: {
				Description: "the origin the deployment is reached at from outside, without a " +
					"path, which redirects, cookies and token issuers are built from",
			},
			trustedOriginsItem: {
				Origins: config.OriginPrimary,
				Description: "origins, each scheme://host[:port], that the cross-origin check " +
					"trusts besides the origin of the public URL",
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
