package webauthn

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

const (
	rpIDItem    = "rp-id"
	rpNameItem  = "rp-name"
	originsItem = "origins"
)

const (
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"
)

const rpNameDefault = "speed"

type moduleConfig struct {
	RPID    string   `config:"rp-id"`
	RPName  string   `config:"rp-name"`
	Origins []string `config:"origins"`
}

var configDefaults = moduleConfig{RPName: rpNameDefault}

type gateMount struct {
	PublicURL string `config:"public-url"`
}

type resolved struct {
	rpID    string
	rpName  string
	origins []string
}

func (c moduleConfig) resolve(gm gateMount) (resolved, error) {
	publicURL := strings.TrimSpace(gm.PublicURL)
	if publicURL == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the relying party id and the origins default to it",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem)
	}

	host := hostOf(publicURL)
	if host == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s does not carry a host to check the relying party id against",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem)
	}
	origin, err := mount.Origin(publicURL)
	if err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s is not an origin: %w",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem, err)
	}

	r := resolved{
		rpID:   strings.TrimSpace(c.RPID),
		rpName: strings.TrimSpace(c.RPName),
	}
	if r.rpName == "" {
		r.rpName = rpNameDefault
	}
	if r.rpID == "" {
		r.rpID = host
	}

	for _, o := range c.Origins {
		if o = strings.TrimSpace(o); o != "" {
			r.origins = append(r.origins, o)
		}
	}
	if len(r.origins) == 0 {
		r.origins = []string{origin}
	}

	if !withinHost(r.rpID, host) {
		return resolved{}, fmt.Errorf("%w: %s.%s is %q, which is neither the host %q of %s.%s nor a parent "+
			"domain of it", authn.ErrInvalidConfig, ConfigNamespace, rpIDItem, r.rpID, host, gateNamespace, gatePublicURLItem)
	}
	return r, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func withinHost(rpID, host string) bool {
	if rpID == "" {
		return false
	}
	if rpID == host {
		return true
	}
	return strings.HasSuffix(host, "."+rpID)
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			rpIDItem: {
				Description: "the relying party identifier a credential is bound to; empty takes the host of " +
					"the public address, and the value must be that host or a parent domain of it",
			},
			rpNameItem: {
				Description: "the relying party's display name, shown by the authenticator beside the account",
			},
			originsItem: {
				Description: "the origins the relying party accepts an assertion from; empty takes the origin " +
					"of the public address",
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
	if err := reader.Decode(gateNamespace+"."+gateMountPath, &gm); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(gm)
}
