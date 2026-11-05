package invitemail

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The key this module accepts, relative to its namespace, and the full path a
// failure names.
const (
	acceptBaseURLItem = "accept-base-url"
	acceptBaseURLKey  = ConfigNamespace + "." + acceptBaseURLItem
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// AcceptBaseURL is the absolute origin the invitation landing is served at.
	// The notice's link is this origin plus the public acceptance path. An empty
	// value is a legal deployment that carries the module but sends no mail: a
	// delivery then reports not_configured.
	AcceptBaseURL string `config:"accept-base-url"`
}

// configDefaults is what the module is worth without a configuration file: no
// origin, so no notice leaves the process.
var configDefaults = moduleConfig{}

// resolved is the configuration this module runs on. An empty acceptBaseURL is
// the not-configured state, not an error.
type resolved struct {
	acceptBaseURL string
}

// resolve turns the decoded configuration into what the module runs on. An
// empty item is the not-configured state; anything else has to be an absolute
// http or https URL with a host and no query or fragment, because the notice's
// link joins the public acceptance path to it. The returned origin is
// canonical: its scheme is lower case and its path carries no trailing slash,
// so the join never writes a doubled slash.
//
// The failure names the item, never the refused value: a deployment may write a
// URL carrying a credential, and a startup failure is not the place to repeat
// it.
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

// configSchema declares this module's config section: one input item, the
// origin the acceptance landing is served at.
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

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
