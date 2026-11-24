package github

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

const (
	clientIDItem      = "client-id"
	clientSecretItem  = "client-secret"
	authorizeURLItem  = "authorize-url"
	tokenURLItem      = "token-url"
	apiBaseItem       = "api-base"
	allowLoopbackItem = "allow-loopback"
	rootCAFileItem    = "root-ca-file"
)

// configDefaults is empty: an application nobody registered cannot complete a login.
var configDefaults = moduleConfig{}

type moduleConfig struct {
	ClientID string `config:"client-id"`
	// ClientSecret signs the token exchange and is registered as a literal redaction rule.
	ClientSecret string `config:"client-secret"`
	AuthorizeURL string `config:"authorize-url"`
	TokenURL     string `config:"token-url"`
	// APIBase, when non-empty, replaces the base the two reads resolve against. A GitHub
	// Enterprise base carries its own path; trailing slashes are removed on read.
	APIBase string `config:"api-base"`
	// AllowLoopback admits loopback addresses through the restricted egress client; it is a
	// development relaxation, off in production.
	AllowLoopback bool `config:"allow-loopback"`
	// RootCAFile is a PEM file whose certificates are appended to the system roots, not a
	// replacement for them; empty uses the system roots alone.
	RootCAFile string `config:"root-ca-file"`
}

type gateConfig struct {
	Mount struct {
		PublicURL string `config:"public-url"`
		Bases     struct {
			Default string `config:"default"`
		} `config:"bases"`
	} `config:"mount"`
}

type resolved struct {
	clientID      string
	clientSecret  string
	redirectURI   string
	endpoint      oauth2.Endpoint
	apiBase       string
	allowLoopback bool
	rootCAs       *x509.CertPool
}

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
				Description: "a PEM file whose certificates are appended to the system roots the provider's " +
					"certificate is verified against, not a replacement for them; empty uses the system roots alone",
			},
		},
	}
}

type clientIDOnly struct {
	ClientID string `config:"client-id"`
}

type clientSecretOnly struct {
	ClientSecret string `config:"client-secret"`
}

// credentialGiven reports whether the loader was given a value for one required credential
// item. ErrMissingRequired means no source gave it; any other failure is carried out as it stands.
func credentialGiven(reader config.Reader, target any) (bool, error) {
	err := reader.Decode(ConfigNamespace, target)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, config.ErrMissingRequired):
		return false, nil
	default:
		return false, err
	}
}

// halfConfigured is the failure of a deployment that supplied one credential and not the
// other, naming the item to write.
func halfConfigured(item string) error {
	return fmt.Errorf("%w: %s.%s was not given while the other credential was; the GitHub "+
		"application needs both the client identifier and the client secret, so give both or neither",
		authn.ErrInvalidConfig, ConfigNamespace, item)
}

// readConfig decodes this module's section and the gateway keys the callback address is
// built from. It refuses an empty public base or mount path, a public base that is not an
// origin, a non-https endpoint and a root CA file that cannot be read; Prepare and New share it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the token exchange names the "+
			"application by it", authn.ErrInvalidConfig, ConfigNamespace, clientIDItem)
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the token exchange authenticates "+
			"the application with it", authn.ErrInvalidConfig, ConfigNamespace, clientSecretItem)
	}
	var gate gateConfig
	if err := reader.Decode(gateNamespace, &gate); err != nil {
		return resolved{}, err
	}
	if gate.Mount.PublicURL == "" {
		return resolved{}, fmt.Errorf("%w: %s.mount.public-url is empty, and the callback address the "+
			"provider is told is built from it", authn.ErrInvalidConfig, gateNamespace)
	}
	if _, err := mount.Origin(gate.Mount.PublicURL); err != nil {
		return resolved{}, fmt.Errorf("%w: %s.mount.public-url is not an origin: %w",
			authn.ErrInvalidConfig, gateNamespace, err)
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
		// The two reads join a path beginning with a slash, so the base is normalized once here to
		// end without one.
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
		clientID:      clientID,
		clientSecret:  cfg.ClientSecret,
		redirectURI:   gate.Mount.PublicURL + gate.Mount.Bases.Default + callbackPath,
		endpoint:      endpoint,
		apiBase:       apiBase,
		allowLoopback: cfg.AllowLoopback,
		rootCAs:       roots,
	}, nil
}

// checkEndpointURL holds a configured endpoint to an absolute https address with a host.
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
