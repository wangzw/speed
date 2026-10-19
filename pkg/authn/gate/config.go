package gate

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace.
const (
	endpointsItem      = "endpoints"
	budgetItem         = "call-budget"
	inFlightItem       = "in-flight"
	mountEndpointItem  = "mount.endpoint"
	mountBasesItem     = "mount.bases"
	mountPublicURLItem = "mount.public-url"
	mountInsecureItem  = "mount.allow-insecure"
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

// moduleConfig is the set of input items this module accepts.
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

// mountConfig is the mount.* section of the configuration.
type mountConfig struct {
	// Endpoint is the endpoint the capability's own routes are mounted on.
	// Empty means the one endpoint the entry point declares, when it declares
	// exactly one.
	Endpoint string `config:"endpoint"`
	// Bases is the base path of each scope the capability's routes are mounted
	// under: the levels are published under different path segments.
	Bases bases `config:"bases"`
	// PublicURL is the origin the deployment is reached at from outside, which
	// the entry point cannot derive from a request.
	PublicURL string `config:"public-url"`
	// AllowInsecure admits an http public URL, for development.
	AllowInsecure bool `config:"allow-insecure"`
}

// bases is the base path of the ordinary surface and of each management scope
// the capability's routes are published under, written as bare literal paths.
type bases struct {
	Default string `config:"default"`
	Tenants string `config:"tenants"`
	System  string `config:"system"`
}

// settings is the configuration this module actually runs on.
type settings struct {
	// endpoints is the list as written, which may be the single reserved name.
	endpoints      []string
	budget         time.Duration
	inFlight       int
	mount          mountConfig
	trustedOrigins []string
}

// allGuarded reports whether the list is the reserved name alone.
func (s settings) allGuarded() bool {
	return len(s.endpoints) == 1 && s.endpoints[0] == allEndpoints
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on.
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
	if err := checkPublicURL(s.mount.PublicURL, s.mount.AllowInsecure); err != nil {
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

// checkEndpoints holds the endpoint list to its rules: it names something, the
// reserved name stands alone, and no name is empty or written twice.
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

// checkBasePath holds a mount path to the form gin keeps a path in: it begins
// with a slash, does not end with one, carries no wildcard or parameter and is
// already what path.Clean would make of it.
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
// is a loopback one or insecure use was allowed. Empty is allowed here: the
// modules that need the address refuse it themselves.
//
// A URL that does not parse is not quoted, and one that does is quoted without
// its user information, query or fragment: the value may carry a password written
// where no password belongs, and the refusal must not copy it into an error text.
func checkPublicURL(raw string, allowInsecure bool) error {
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
	if u.Scheme == "http" && !allowInsecure && !loopbackHost(u.Hostname()) {
		return fmt.Errorf("%w: %s.%s is %q, which is not https. Cookies and redirects built on it "+
			"would travel in the clear. Use https, or allow insecure use, which is for "+
			"development, with %s.%s", authn.ErrInsecureConfig, ConfigNamespace, mountPublicURLItem,
			shown, ConfigNamespace, mountInsecureItem)
	}
	return nil
}

// withoutUser renders a parsed URL with its user information, query and fragment
// removed. Those are the parts of a URL a secret rides in.
func withoutUser(u *url.URL) string {
	bare := *u
	bare.User = nil
	bare.RawQuery = ""
	bare.ForceQuery = false
	bare.Fragment = ""
	bare.RawFragment = ""
	return bare.String()
}

// loopbackHost reports whether host names this machine's loopback interface.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// schema declares this module's input items.
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
			mountEndpointItem: {
				Description: "the endpoint the authentication routes are mounted on; empty " +
					"takes the entry point's only endpoint",
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
			mountInsecureItem: {
				Origins: config.OriginPrimary,
				Description: "for development only: admit an http public URL and drop the " +
					"__Host- prefix and the Secure attribute from cookies",
			},
			trustedOriginsItem: {
				Origins: config.OriginPrimary,
				Description: "origins, each scheme://host[:port], that the cross-origin check " +
					"trusts besides the origin of the public URL",
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
