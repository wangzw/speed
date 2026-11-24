package invitemail

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

const (
	acceptBaseURLItem = "accept-base-url"
	acceptBaseURLKey  = ConfigNamespace + "." + acceptBaseURLItem
)

type moduleConfig struct {
	AcceptBaseURL string `config:"accept-base-url"`
}

var configDefaults = moduleConfig{}

type resolved struct {
	acceptBaseURL string
}

func (c moduleConfig) resolve() (resolved, error) {
	raw := strings.TrimSpace(c.AcceptBaseURL)
	if raw == "" {
		return resolved{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" ||
		(!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) {
		return resolved{}, fmt.Errorf("%w: %s: not an absolute http or https URL",
			authn.ErrInvalidConfig, acceptBaseURLKey)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return resolved{}, fmt.Errorf("%w: %s: a base URL carries neither a query nor a fragment",
			authn.ErrInvalidConfig, acceptBaseURLKey)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return resolved{acceptBaseURL: u.String()}, nil
}

func configSchema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			acceptBaseURLItem: {
				Description: "the absolute http or https origin the acceptance landing is served at; " +
					"an invitation notice links to <accept-base-url>/invitations/<token>, and an empty " +
					"value sends no mail and reports not_configured",
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
