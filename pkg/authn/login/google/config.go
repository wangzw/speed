package google

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The configuration keys this module accepts, relative to its namespace. They
// are configuration and not settings because the provider is built at
// construction, before any database or settings store is reachable.
const (
	clientIDItem     = "client-id"
	clientSecretItem = "client-secret"
	hostedDomainItem = "hosted-domain"
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
	clientID     string
	clientSecret string
	hostedDomain string
	redirectURL  string
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
	return resolved{
		clientID:     clientID,
		clientSecret: c.ClientSecret,
		hostedDomain: strings.TrimSpace(c.HostedDomain),
		redirectURL:  redirectTarget(publicURL, gm.Bases.Default),
	}, nil
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
		},
	}
}

// readConfig decodes this module's section and the gateway's mount items, and
// validates both. Prepare and New both go through it, so the two see one
// configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	var gm gateMount
	if err := reader.Decode(gateNamespace+"."+gateMountPath, &gm); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(gm)
}
