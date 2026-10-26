package google

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The configuration keys this module accepts, relative to its namespace. They
// are configuration and not settings because the provider is built at
// construction, before any database or settings store is reachable.
const (
	clientIDItem      = "client-id"
	clientSecretItem  = "client-secret"
	hostedDomainItem  = "hosted-domain"
	issuerItem        = "issuer"
	allowLoopbackItem = "allow-loopback"
	rootCAFileItem    = "root-ca-file"
)

// gateNamespace and the two mount items the gateway declares, read here because
// the redirect target is built from the address the deployment is reached at
// and the base path the routes are mounted under.
const (
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"
	gateBasePathItem  = "mount.bases.default"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// ClientID is the OAuth client identifier the provider issued.
	ClientID string `config:"client-id"`
	// ClientSecret is the OAuth client secret the provider issued.
	ClientSecret string `config:"client-secret"`
	// HostedDomain, when non-empty, is the Google Workspace domain the identity
	// token's hd claim must equal. It confines the identity to an organization;
	// the hd parameter on the authorization request is a display optimization
	// and is not the check.
	HostedDomain string `config:"hosted-domain"`
	// Issuer, when non-empty, is the OpenID Connect issuer the provider is
	// discovered at. Empty discovers the built-in Google issuer.
	Issuer string `config:"issuer"`
	// AllowLoopback admits loopback addresses through the restricted egress
	// client. It is a development and local-testing relaxation; production
	// leaves it off.
	AllowLoopback bool `config:"allow-loopback"`
	// RootCAFile is a PEM file whose certificates are appended to the system
	// roots the provider's certificate is verified against, not a replacement
	// for them. Empty uses the system roots alone.
	RootCAFile string `config:"root-ca-file"`
}

// configDefaults is what this module is worth without a configuration file. The
// client identifier and the secret have none.
var configDefaults = moduleConfig{}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
	Bases     struct {
		Default string `config:"default"`
	} `config:"bases"`
}

// resolved is the configuration the method runs on.
type resolved struct {
	clientID      string
	clientSecret  string
	hostedDomain  string
	redirectURL   string
	issuer        string
	allowLoopback bool
	rootCAFile    string
	rootCAs       *x509.CertPool
}

// resolve holds the configuration to what the flow and the redirect target are
// built from, or says which item to correct. The client identifier, the secret
// and the public address are all required: the secret is needed to exchange the
// authorization code, and the redirect target is the public address plus the
// base path plus this provider's callback path.
func (c moduleConfig) resolve(gm gateMount) (resolved, error) {
	clientID := strings.TrimSpace(c.ClientID)
	if clientID == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty", authn.ErrInvalidConfig, ConfigNamespace, clientIDItem)
	}
	if strings.TrimSpace(c.ClientSecret) == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty", authn.ErrInvalidConfig, ConfigNamespace, clientSecretItem)
	}
	publicURL := strings.TrimSpace(gm.PublicURL)
	if publicURL == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the redirect target is built from it",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem)
	}
	if _, err := mount.Origin(publicURL); err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s is not an origin: %w",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem, err)
	}
	issuer, err := resolveIssuer(c.Issuer, c.AllowLoopback)
	if err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s: %w", authn.ErrInvalidConfig, ConfigNamespace, issuerItem, err)
	}
	return resolved{
		clientID:      clientID,
		clientSecret:  c.ClientSecret,
		hostedDomain:  strings.TrimSpace(c.HostedDomain),
		redirectURL:   redirectTarget(publicURL, gm.Bases.Default),
		issuer:        issuer,
		allowLoopback: c.AllowLoopback,
		rootCAFile:    strings.TrimSpace(c.RootCAFile),
	}, nil
}

// resolveIssuer returns the issuer the provider is discovered at. An empty value
// takes the built-in Google issuer. An explicit value is an absolute https
// address; a plain http address is accepted only on a loopback host with the
// loopback switch on, because that is the development shape of a provider
// running on the same machine.
func resolveIssuer(raw string, allowLoopback bool) (string, error) {
	issuer := strings.TrimSpace(raw)
	if issuer == "" {
		return defaultIssuer, nil
	}
	u, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("it is not a URL: %w", err)
	}
	if u.Host == "" {
		return "", errors.New("it names no host")
	}
	switch u.Scheme {
	case "https":
		return issuer, nil
	case "http":
		if !allowLoopback {
			return "", errors.New("it is not https")
		}
		if !loopbackHost(u.Hostname()) {
			return "", errors.New("a plain http issuer is only accepted on a loopback host with the loopback switch on")
		}
		return issuer, nil
	default:
		return "", fmt.Errorf("scheme %q is not https", u.Scheme)
	}
}

// loopbackHost reports whether host is the literal localhost name or a loopback
// address.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// redirectTarget is the redirect URI the provider sends the user agent back to:
// the public address, the base path the routes are mounted under, and this
// provider's callback path. A base path the gateway did not set reads as the
// declared default, so the target agrees with where the engine mounted the
// callback.
func redirectTarget(publicURL, basePath string) string {
	base := strings.TrimSpace(basePath)
	if base == "" {
		base = mount.DefaultBasePath
	}
	return strings.TrimRight(publicURL, "/") + "/" + strings.Trim(base, "/") + callbackPath
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			clientIDItem: {
				Required:    true,
				Description: "the OAuth client identifier the deployment registered with the provider",
			},
			clientSecretItem: {
				Required:    true,
				Sensitive:   true,
				Description: "the OAuth client secret the deployment registered with the provider",
			},
			hostedDomainItem: {
				Description: "a Google Workspace domain the identity token's hd claim must equal; empty accepts an " +
					"identity from any domain",
			},
			issuerItem: {
				Description: "the OpenID Connect issuer the provider is discovered at; empty uses " + defaultIssuer,
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

// readConfig decodes this module's section and the gateway's mount items, and
// validates both. Prepare and New both go through it, so the two see one
// configuration. A configured root CA file is read here, so a file that does not
// work stops the start rather than leaving verification on the system roots
// without saying so.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	var gm gateMount
	if err := reader.Decode(gateNamespace+"."+gateMountPath, &gm); err != nil {
		return resolved{}, err
	}
	r, err := cfg.resolve(gm)
	if err != nil {
		return resolved{}, err
	}
	roots, err := egress.RootCAsFromFile(r.rootCAFile)
	if err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s: %w", authn.ErrInvalidConfig, ConfigNamespace, rootCAFileItem, err)
	}
	r.rootCAs = roots
	return r, nil
}
