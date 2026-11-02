package totp

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// issuerItem is this module's input item, relative to its namespace. It is
// configuration and not a setting: it is read before a database is reachable and
// the otpauth address is built from it.
const issuerItem = "issuer"

// gateNamespace and gatePublicURLItem are the gateway's, read here because the
// issuer defaults to the host name of the address the deployment is reached at.
const (
	gateNamespace     = "authn.gate"
	gatePublicURLItem = "mount.public-url"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Issuer is the site name an authenticator app shows beside the account,
	// which the deployer sets. Empty takes the host name of the gateway's public
	// address.
	Issuer string `config:"issuer"`
}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	issuer string
}

// resolve fills the issuer from the gateway's public address when it is not
// given. It is the host name alone, without a scheme, a port or a path: that is
// what an authenticator app displays.
func (c moduleConfig) resolve(gm gateMount) (resolved, error) {
	issuer := strings.TrimSpace(c.Issuer)
	if issuer == "" {
		host, err := hostOf(strings.TrimSpace(gm.PublicURL))
		if err != nil {
			return resolved{}, fmt.Errorf("%w: %s.%s is empty and %s.%s gives no host to default it to: %w",
				authn.ErrInvalidConfig, ConfigNamespace, issuerItem, gateNamespace, gatePublicURLItem, err)
		}
		issuer = host
	}
	if issuer == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty and no %s.%s is configured to default it to",
			authn.ErrInvalidConfig, ConfigNamespace, issuerItem, gateNamespace, gatePublicURLItem)
	}
	return resolved{issuer: issuer}, nil
}

// hostOf is the host name of a public address. An empty address has no host,
// which is what a module that needs one refuses on.
func hostOf(publicURL string) (string, error) {
	if publicURL == "" {
		return "", nil
	}
	u, err := url.Parse(publicURL)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &moduleConfig{}}},
		Items: map[string]config.Item{
			issuerItem: {
				Description: "the site name an authenticator app shows beside the account, set by the " +
					"deployer; empty takes the host name of the gateway's public address",
			},
		},
	}
}

// readConfig decodes this module's section and the gateway's public address and
// validates both. Prepare and New both go through it, so the two see one
// configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	var gm gateMount
	if err := reader.Decode(gateNamespace+".mount", &gm); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(gm)
}
