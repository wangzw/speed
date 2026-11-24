package google

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

const (
	clientIDItem      = "client-id"
	clientSecretItem  = "client-secret"
	hostedDomainItem  = "hosted-domain"
	issuerItem        = "issuer"
	allowLoopbackItem = "allow-loopback"
	rootCAFileItem    = "root-ca-file"
)

const (
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"
	gateBasePathItem  = "mount.bases.default"
)

type moduleConfig struct {
	ClientID     string `config:"client-id"`
	ClientSecret string `config:"client-secret"`
	// HostedDomain, when non-empty, is the Workspace domain the identity token's hd claim must
	// equal; the hd authorization parameter is a display optimization, not the check.
	HostedDomain string `config:"hosted-domain"`
	// Issuer, when non-empty, is the OpenID Connect issuer the provider is discovered at; empty
	// discovers the built-in Google issuer.
	Issuer string `config:"issuer"`
	// AllowLoopback admits loopback addresses through the restricted egress client; it is a
	// development relaxation, off in production.
	AllowLoopback bool `config:"allow-loopback"`
	// RootCAFile is a PEM file whose certificates are appended to the system roots, not a
	// replacement for them; empty uses the system roots alone.
	RootCAFile string `config:"root-ca-file"`
}

// configDefaults is empty: the client identifier and the secret have no default.
var configDefaults = moduleConfig{}

type gateMount struct {
	PublicURL string `config:"public-url"`
	Bases     struct {
		Default string `config:"default"`
	} `config:"bases"`
}

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

// resolve holds the configuration to what the flow and the redirect target are built from,
// or says which item to correct. The client identifier, the secret and the public address are
// all required.
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

// resolveIssuer returns the issuer the provider is discovered at. An explicit value must be
// absolute https; http is accepted only on a loopback host with the loopback switch on.
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
		if !cookiename.LoopbackHost(u.Hostname()) {
			return "", errors.New("a plain http issuer is only accepted on a loopback host with the loopback switch on")
		}
		return issuer, nil
	default:
		return "", fmt.Errorf("scheme %q is not https", u.Scheme)
	}
}

// redirectTarget is the public address, the mounted base path and this provider's callback
// path. A base path the gateway did not set reads as the declared default.
func redirectTarget(publicURL, basePath string) string {
	base := strings.TrimSpace(basePath)
	if base == "" {
		base = mount.DefaultBasePath
	}
	return strings.TrimRight(publicURL, "/") + "/" + strings.Trim(base, "/") + callbackPath
}

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

// The two required credential items, each read on its own so the module can tell a deployment
// that configured neither from one that configured only half.
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
	return fmt.Errorf("%w: %s.%s was not given while the other credential was; the Google "+
		"application needs both the client identifier and the client secret, so give both or neither",
		authn.ErrInvalidConfig, ConfigNamespace, item)
}

// readConfig decodes this module's section and the gateway's mount items and validates both.
// Prepare and New share it, and a root CA file that cannot be read stops the start.
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
