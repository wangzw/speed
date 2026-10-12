package github

import (
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace. Both are
// configuration, read once at startup: they are an OAuth application's
// credentials and are fixed for the life of the process.
const (
	clientIDItem     = "client-id"
	clientSecretItem = "client-secret"
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
}

// gateConfig is the part of the gateway's section this module reads: the public
// base the callback address is built from and the path the capability's routes
// are mounted under.
type gateConfig struct {
	Mount struct {
		PublicURL string `config:"public-url"`
		BasePath  string `config:"base-path"`
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
	apiBase string
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
// guessed from the request.
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
	if gate.Mount.BasePath == "" {
		return resolved{}, fmt.Errorf("%w: %s.mount.base-path is empty, and the callback path is built "+
			"from it", authn.ErrInvalidConfig, gateNamespace)
	}
	return resolved{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURI:  gate.Mount.PublicURL + gate.Mount.BasePath + callbackPath,
		endpoint:     endpoints.GitHub,
		apiBase:      githubAPIBase,
	}, nil
}
