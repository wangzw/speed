package totp

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

const issuerItem = "issuer"

const (
	gateNamespace     = "authn.gate"
	gatePublicURLItem = "mount.public-url"
)

type moduleConfig struct {
	Issuer string `config:"issuer"`
}

type gateMount struct {
	PublicURL string `config:"public-url"`
}

type resolved struct {
	issuer string
}

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
