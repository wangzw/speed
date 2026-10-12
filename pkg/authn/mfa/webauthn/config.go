package webauthn

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The configuration keys this module accepts, relative to its namespace. They
// are configuration and not settings because the relying party is built at
// construction, before a database is reachable.
const (
	rpIDItem    = "rp-id"
	rpNameItem  = "rp-name"
	originsItem = "origins"
)

// gateNamespace and its public-url item are the gateway's, read here because
// the relying party id and the origins default to the address the deployment is
// reached at.
const (
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"
)

// rpNameDefault is the relying party's display name when the configuration names
// none. It is what an authenticator shows next to the account.
const rpNameDefault = "speed"

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// RPID is the relying party identifier. Empty takes the host of the public
	// address; it must be that host or a parent domain of it.
	RPID string `config:"rp-id"`
	// RPName is the relying party's display name. Empty takes the fixed default.
	RPName string `config:"rp-name"`
	// Origins are the origins the relying party accepts. Empty takes the origin
	// of the public address.
	Origins []string `config:"origins"`
}

// configDefaults is what this module is worth without a configuration file. The
// relying party id and the origins default off the gateway's public address, so
// they are resolved rather than held here.
var configDefaults = moduleConfig{RPName: rpNameDefault}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
}

// resolved is the configuration the relying party is built from.
type resolved struct {
	rpID    string
	rpName  string
	origins []string
}

// resolve turns decoded configuration into what the relying party runs on, or
// says which item to correct. The relying party id and the origins are filled
// from the public address when they are not given, and the relying party id is
// held to the public address's host or a parent domain of it: a credential is
// bound to the relying party id, so one that does not cover the host would let
// an assertion collected at the site complete a ceremony begun elsewhere.
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

// hostOf is the lower-case host of a URL, without a port, or the empty string
// when the URL carries none.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// withinHost reports whether rpID is host or a parent domain of it: an equal
// string, or a suffix of host that begins at a label boundary. The public suffix
// table is not consulted, which is the residual risk the design accepts.
func withinHost(rpID, host string) bool {
	if rpID == "" {
		return false
	}
	if rpID == host {
		return true
	}
	return strings.HasSuffix(host, "."+rpID)
}

// schema declares this module's input items.
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

// readConfig decodes this module's section and the gateway's public address, and
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
