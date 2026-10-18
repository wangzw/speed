package mount

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// gateNamespace is where the keys this module reads are declared. They are the
// gateway's: it needs the public URL and the trusted origins for its own
// cross-origin check, so it declares them and this module, like every module
// that needs the public URL, reads them in its own Prepare. This module declares
// no input item.
const gateNamespace = "authn.gate"

// The keys this module reads, relative to gateNamespace.
const (
	endpointItem       = "mount.endpoint"
	basePathItem       = "mount.base-path"
	publicURLItem      = "mount.public-url"
	insecureItem       = "mount.allow-insecure"
	trustedOriginsItem = "trusted-origins"
)

// settings is the part of the gateway's section this module reads.
type settings struct {
	Mount struct {
		Endpoint      string `config:"endpoint"`
		BasePath      string `config:"base-path"`
		PublicURL     string `config:"public-url"`
		AllowInsecure bool   `config:"allow-insecure"`
	} `config:"mount"`
	TrustedOrigins []string `config:"trusted-origins"`
}

// readSettings decodes the gateway's keys and holds them to what this module
// can run on. Prepare and New both go through it, so the two see one
// configuration.
func readSettings(reader config.Reader) (settings, error) {
	var s settings
	// The default is the one the gateway declares, read from the shared value:
	// when the gateway module is in the assembly it declares mount.base-path
	// and the decode fills it, and when a host supplies a gateway of its own
	// nobody declares the keys and this is what the base path is.
	s.Mount.BasePath = mount.DefaultBasePath
	if err := reader.Decode(gateNamespace, &s); err != nil {
		return settings{}, err
	}
	return s, s.check()
}

// key is the full path of an item the gateway declares, for a message.
func key(item string) string { return gateNamespace + "." + item }

// check refuses what this module cannot mount routes on. The gateway judges the
// same keys when it is in the assembly; a host that supplies a gateway of its
// own leaves nobody else to, and the base path and the public URL are what this
// module hands to every module that mounts through it.
//
// A value that does not parse is not quoted, and one that does is quoted without
// its user information, query or fragment: it may carry a password written where
// none belongs.
func (s settings) check() error {
	if err := checkBasePath(s.Mount.BasePath); err != nil {
		return err
	}
	if _, err := mount.TrustedOrigins(s.Mount.PublicURL, s.TrustedOrigins); err != nil {
		return fmt.Errorf("%w: %s or %s is not an origin the cross-origin check can trust: %w",
			authn.ErrInvalidConfig, key(publicURLItem), key(trustedOriginsItem), err)
	}
	return checkPublicURL(s.Mount.PublicURL, s.Mount.AllowInsecure)
}

// checkBasePath holds the base path to the form gin keeps a path in: it begins
// with a slash, does not end with one, carries no wildcard or parameter and is
// what path.Clean would make of it.
func checkBasePath(p string) error {
	switch {
	case !strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/"):
		return fmt.Errorf("%w: %s is %q, which has to begin with a slash and not end with one, "+
			"such as /api/v1/auth", authn.ErrInvalidConfig, key(basePathItem), clip(p))
	case strings.ContainsAny(p, "*:?#{}"):
		return fmt.Errorf("%w: %s is %q, which carries a wildcard, a parameter, a query or a "+
			"fragment, and routes are mounted under a literal path", authn.ErrInvalidConfig,
			key(basePathItem), clip(p))
	case path.Clean(p) != p:
		return fmt.Errorf("%w: %s is %q, which is not in the cleaned form gin keeps a path in",
			authn.ErrInvalidConfig, key(basePathItem), clip(p))
	}
	return nil
}

// checkPublicURL holds the public URL to an origin without a path, and to https
// unless its host is a loopback one or insecure use was allowed. Empty is
// allowed: the modules that need the address refuse it themselves. The origin's
// own shape is judged by mount.TrustedOrigins before this runs.
func checkPublicURL(raw string, allowInsecure bool) error {
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
			"the path is %s", authn.ErrInvalidConfig, key(publicURLItem), shown, key(basePathItem))
	}
	if u.Scheme == "http" && !allowInsecure && !loopbackHost(u.Hostname()) {
		return fmt.Errorf("%w: %s is %q, which is not https. Cookies and redirects built on it would "+
			"travel in the clear. Use https, or allow insecure use, which is for development, with %s",
			authn.ErrInsecureConfig, key(publicURLItem), shown, key(insecureItem))
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

// loopbackHost reports whether host names this machine's loopback interface.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
