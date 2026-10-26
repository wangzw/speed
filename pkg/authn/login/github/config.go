package github

import (
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace. They are
// configuration, read once at startup: the credentials and endpoints are fixed
// for the life of the process.
const (
	clientIDItem      = "client-id"
	clientSecretItem  = "client-secret"
	authorizeURLItem  = "authorize-url"
	tokenURLItem      = "token-url"
	apiBaseItem       = "api-base"
	allowLoopbackItem = "allow-loopback"
	rootCAFileItem    = "root-ca-file"
)

// configDefaults is what this module is worth without a configuration file:
// nothing. There is no default credential, because an application nobody
// registered cannot complete a login.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// ClientID is the OAuth application's client identifier.
	ClientID string `config:"client-id"`
	// ClientSecret is the OAuth application's client secret. It signs the
	// token exchange and is registered as a literal redaction rule.
	ClientSecret string `config:"client-secret"`
	// AuthorizeURL, when non-empty, replaces GitHub's authorization endpoint.
	AuthorizeURL string `config:"authorize-url"`
	// TokenURL, when non-empty, replaces GitHub's token endpoint.
	TokenURL string `config:"token-url"`
	// APIBase, when non-empty, replaces the base the two reads are resolved
	// against. A GitHub Enterprise API base carries its own path; trailing
	// slashes are removed on read so the joined read path has exactly one.
	APIBase string `config:"api-base"`
	// AllowLoopback admits loopback addresses through the restricted egress
	// client. It is a development and local-testing relaxation; production
	// leaves it off.
	AllowLoopback bool `config:"allow-loopback"`
	// RootCAFile is a PEM file whose certificates are appended to the system
	// roots the provider's certificate is verified against, not a replacement
	// for them. Empty uses the system roots alone.
	RootCAFile string `config:"root-ca-file"`
}

// gateConfig is the part of the gateway's section this module reads: the public
// base the callback address is built from and the path the capability's routes
// are mounted under.
type gateConfig struct {
	Mount struct {
		PublicURL string `config:"public-url"`
		Bases     struct {
			Default string `config:"default"`
		} `config:"bases"`
	} `config:"mount"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	clientID     string
	clientSecret string
	// redirectURI is the callback address the provider is told to send the
	// browser back to: the public base, the mount path and the callback route,
	// which ends in this method's name.
	redirectURI string
	endpoint    oauth2.Endpoint
	// apiBase is the base the two reads are resolved against.
	apiBase       string
	allowLoopback bool
	rootCAs       *x509.CertPool
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			clientIDItem: {
				Required:    true,
				Description: "the GitHub OAuth application's client identifier",
			},
			clientSecretItem: {
				Required:    true,
				Sensitive:   true,
				Description: "the GitHub OAuth application's client secret",
			},
			authorizeURLItem: {
				Description: "the authorization endpoint; empty uses " + endpoints.GitHub.AuthURL,
			},
			tokenURLItem: {
				Description: "the token endpoint; empty uses " + endpoints.GitHub.TokenURL,
			},
			apiBaseItem: {
				Description: "the base the account and address reads are resolved against; empty uses " +
					githubAPIBase,
			},
			allowLoopbackItem: {
				Description: "whether the restricted egress client admits loopback addresses, for a provider " +
					"running on the same machine; production leaves it off",
			},
			rootCAFileItem: {
				Description: "a PEM file whose certificates replace the system roots the provider's certificate is " +
					"verified against; empty uses the system roots",
			},
		},
	}
}

// readConfig decodes this module's section and the gateway keys the callback
// address is built from. Prepare and New both go through it, so the two see one
// mapping.
//
// The public base and the mount path are refused when empty: the provider is
// told one callback address and the deployment must reach the route it names, so
// a configuration that cannot build it is a startup failure, never an address
// guessed from the request. The endpoint overrides are refused unless they are
// absolute https addresses, and a configured root CA file is read here, so a file
// that does not work stops the start rather than leaving verification on the
// system roots without saying so.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	var gate gateConfig
	if err := reader.Decode(gateNamespace, &gate); err != nil {
		return resolved{}, err
	}
	if gate.Mount.PublicURL == "" {
		return resolved{}, fmt.Errorf("%w: %s.mount.public-url is empty, and the callback address the "+
			"provider is told is built from it", authn.ErrInvalidConfig, gateNamespace)
	}
	if gate.Mount.Bases.Default == "" {
		return resolved{}, fmt.Errorf("%w: %s.mount.bases.default is empty, and the callback path is built "+
			"from it", authn.ErrInvalidConfig, gateNamespace)
	}
	endpoint := endpoints.GitHub
	if v := strings.TrimSpace(cfg.AuthorizeURL); v != "" {
		if err := checkEndpointURL(authorizeURLItem, v); err != nil {
			return resolved{}, err
		}
		endpoint.AuthURL = v
	}
	if v := strings.TrimSpace(cfg.TokenURL); v != "" {
		if err := checkEndpointURL(tokenURLItem, v); err != nil {
			return resolved{}, err
		}
		endpoint.TokenURL = v
	}
	apiBase := githubAPIBase
	if v := strings.TrimSpace(cfg.APIBase); v != "" {
		// The two reads join a path that begins with a slash, so a base ending
		// in one would produce a doubled slash and an address the provider does
		// not serve. The value is normalized once, here, so every read joins the
		// same way whatever the deployment wrote.
		v = strings.TrimRight(v, "/")
		if err := checkEndpointURL(apiBaseItem, v); err != nil {
			return resolved{}, err
		}
		apiBase = v
	}
	rootCAFile := strings.TrimSpace(cfg.RootCAFile)
	roots, err := egress.RootCAsFromFile(rootCAFile)
	if err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s: %w", authn.ErrInvalidConfig, ConfigNamespace, rootCAFileItem, err)
	}
	return resolved{
		clientID:      cfg.ClientID,
		clientSecret:  cfg.ClientSecret,
		redirectURI:   gate.Mount.PublicURL + gate.Mount.Bases.Default + callbackPath,
		endpoint:      endpoint,
		apiBase:       apiBase,
		allowLoopback: cfg.AllowLoopback,
		rootCAs:       roots,
	}, nil
}

// checkEndpointURL holds a configured endpoint to an absolute https address with
// a host. The restricted egress client speaks https only, so an address of
// another scheme could never be reached; refusing it here names the item to
// correct instead of failing at the first login.
func checkEndpointURL(item, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s.%s: %w", authn.ErrInvalidConfig, ConfigNamespace, item, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %s.%s is not an absolute https address", authn.ErrInvalidConfig, ConfigNamespace, item)
	}
	return nil
}
