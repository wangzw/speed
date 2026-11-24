package mount

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// gateNamespace is where the gateway declares the keys this module reads; this module
// declares no input item.
const gateNamespace = "authn.gate"

const (
	endpointsItem      = "mount.endpoints"
	basesItem          = "mount.bases"
	publicURLItem      = "mount.public-url"
	trustedOriginsItem = "trusted-origins"
)

const (
	defaultMountItem = endpointsItem + ".default"
	tenantsMountItem = endpointsItem + ".tenants"
	systemMountItem  = endpointsItem + ".system"
)

type endpoints struct {
	Default string `config:"default"`
	Tenants string `config:"tenants"`
	System  string `config:"system"`
}

type bases struct {
	Default string `config:"default"`
	Tenants string `config:"tenants"`
	System  string `config:"system"`
}

func defaultBases() bases {
	return bases{
		Default: mount.DefaultBasePath,
		Tenants: mount.DefaultTenantsBasePath,
		System:  mount.DefaultSystemBasePath,
	}
}

type settings struct {
	Mount struct {
		Endpoints endpoints `config:"endpoints"`
		Bases     bases     `config:"bases"`
		PublicURL string    `config:"public-url"`
	} `config:"mount"`
	TrustedOrigins []string `config:"trusted-origins"`
}

// readSettings decodes the gateway's keys and holds them to what this module can run on;
// Prepare and New both go through it.
func readSettings(reader config.Reader) (settings, error) {
	var s settings
	// The defaults are the gateway's declared defaults; a host gateway declares nothing, so these
	// are what the base paths are.
	s.Mount.Bases = defaultBases()
	if err := reader.Decode(gateNamespace, &s); err != nil {
		return settings{}, err
	}
	return s, s.check()
}

func key(item string) string { return gateNamespace + "." + item }

// check refuses what this module cannot mount routes on. The gateway judges the same keys when
// present; a host gateway leaves that to this module. A value that does not parse is not quoted;
// one that does is quoted without user information, query or fragment.
func (s settings) check() error {
	for _, item := range []struct {
		name  string
		value string
	}{
		{"mount.bases.default", s.Mount.Bases.Default},
		{"mount.bases.tenants", s.Mount.Bases.Tenants},
		{"mount.bases.system", s.Mount.Bases.System},
	} {
		if err := checkBasePath(item.name, item.value); err != nil {
			return err
		}
	}
	if _, err := mount.TrustedOrigins(s.Mount.PublicURL, s.TrustedOrigins); err != nil {
		return fmt.Errorf("%w: %s or %s is not an origin the cross-origin check can trust: %w",
			authn.ErrInvalidConfig, key(publicURLItem), key(trustedOriginsItem), err)
	}
	return checkPublicURL(s.Mount.PublicURL)
}

// checkBasePath holds a base path to the form gin keeps a path in: it begins
// with a slash, does not end with one, carries no wildcard or parameter and is
// what path.Clean would make of it.
func checkBasePath(item, p string) error {
	switch {
	case !strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/"):
		return fmt.Errorf("%w: %s is %q, which has to begin with a slash and not end with one, "+
			"such as /api/v1/auth", authn.ErrInvalidConfig, key(item), clip(p))
	case strings.ContainsAny(p, "*:?#{}"):
		return fmt.Errorf("%w: %s is %q, which carries a wildcard, a parameter, a query or a "+
			"fragment, and routes are mounted under a literal path", authn.ErrInvalidConfig,
			key(item), clip(p))
	case path.Clean(p) != p:
		return fmt.Errorf("%w: %s is %q, which is not in the cleaned form gin keeps a path in",
			authn.ErrInvalidConfig, key(item), clip(p))
	}
	return nil
}

// checkPublicURL holds the public URL to an origin without a path, and to https unless its
// host is loopback. Empty is allowed; the modules that need it refuse it themselves.
func checkPublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s is not a URL with a scheme and a host", authn.ErrInvalidConfig, key(publicURLItem))
	}
	shown := clip(withoutUser(u))
	if u.Path != "" {
		return fmt.Errorf("%w: %s is %q, which carries a path. Write scheme, host and port alone: "+
			"the path is %s", authn.ErrInvalidConfig, key(publicURLItem), shown, key(basesItem))
	}
	if u.Scheme == "http" && !cookiename.LoopbackHost(u.Hostname()) {
		return fmt.Errorf("%w: %s is %q, which is not https. Cookies and redirects built on it would "+
			"cross the network in the clear: only a loopback host, whose traffic never leaves the "+
			"machine, is accepted over http. Use https",
			authn.ErrInsecureConfig, key(publicURLItem), shown)
	}
	return nil
}

// withoutUser renders a parsed URL without its user information, query and
// fragment, which are the parts of a URL a secret rides in.
func withoutUser(u *url.URL) string {
	bare := *u
	bare.User = nil
	bare.RawQuery = ""
	bare.ForceQuery = false
	bare.Fragment = ""
	bare.RawFragment = ""
	return bare.String()
}

// clip shortens a value read from configuration before it enters an error, so
// that a long or odd one cannot bloat or reshape the message.
func clip(s string) string {
	const limit = 64
	if len(s) > limit {
		s = s[:limit] + "..."
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}
