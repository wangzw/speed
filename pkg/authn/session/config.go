package session

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace, and the keys of the
// gateway it reads. The gateway declares the second group and this module reads
// them in Prepare, because what the session cookie is called and whether it is
// Secure follow from the public address the deployment is reached at.
const (
	cookieDomainItem = "cookie-domain"
	sameSiteItem     = "same-site"

	gateNamespace     = "authn.gate"
	gatePublicURLItem = "mount.public-url"
	gateInsecureItem  = "mount.allow-insecure"
)

// The cookie names. The prefixes are the browser's own: __Host- makes the
// cookie host-only and Secure, so no subdomain can set it; __Secure- only
// requires Secure and leaves Domain free.
const (
	hostCookieName     = "__Host-sid"
	secureCookieName   = "__Secure-sid"
	insecureCookieName = "sid"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// CookieDomain, when non-empty, is written as the Domain attribute of the
	// session cookie and makes the cookie __Secure-sid, which loses the
	// guarantee that no subdomain can set it.
	CookieDomain string `config:"cookie-domain"`
	// SameSite is lax or strict. none is refused.
	SameSite string `config:"same-site"`
}

// configDefaults is what this module is worth without a configuration file.
var configDefaults = moduleConfig{SameSite: "lax"}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL     string `config:"public-url"`
	AllowInsecure bool   `config:"allow-insecure"`
}

// cookieSpec is what the configuration comes to: the name and the attributes of
// the session cookie, and the diagnostics that follow from them.
type cookieSpec struct {
	name     string
	domain   string
	sameSite http.SameSite
	secure   bool
	// insecureAllowed is the gateway's allow-insecure switch: the cookie has no
	// prefix and is not Secure, for development.
	insecureAllowed bool
}

// domainShape is a host name as a cookie's Domain attribute may carry it:
// letters, digits and hyphens in labels separated by dots.
var domainShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// resolve turns decoded configuration into the cookie this module writes,
// refusing what cannot be run on.
func (c moduleConfig) resolve(gm gateMount) (cookieSpec, error) {
	spec := cookieSpec{insecureAllowed: gm.AllowInsecure}

	switch strings.ToLower(c.SameSite) {
	case "lax":
		spec.sameSite = http.SameSiteLaxMode
	case "strict":
		spec.sameSite = http.SameSiteStrictMode
	case "none":
		return cookieSpec{}, fmt.Errorf("%w: %s.%s is none, which would send the session cookie with "+
			"requests another site causes. Use lax or strict", authn.ErrInsecureConfig, ConfigNamespace, sameSiteItem)
	default:
		return cookieSpec{}, fmt.Errorf("%w: %s.%s is not lax or strict", authn.ErrInvalidConfig,
			ConfigNamespace, sameSiteItem)
	}

	host, secureScheme, err := publicHost(gm.PublicURL)
	if err != nil {
		return cookieSpec{}, err
	}
	if !secureScheme && !gm.AllowInsecure {
		return cookieSpec{}, fmt.Errorf("%w: %s.%s is not https, and the session cookie is Secure and cannot "+
			"be made otherwise. Use https, or allow insecure use, which is for development, with %s.%s",
			authn.ErrInsecureConfig, gateNamespace, gatePublicURLItem, gateNamespace, gateInsecureItem)
	}

	if c.CookieDomain != "" {
		domain := strings.ToLower(strings.TrimPrefix(c.CookieDomain, "."))
		if !domainShape.MatchString(domain) {
			return cookieSpec{}, fmt.Errorf("%w: %s.%s is not a host name", authn.ErrInvalidConfig,
				ConfigNamespace, cookieDomainItem)
		}
		if host != domain && !strings.HasSuffix(host, "."+domain) {
			return cookieSpec{}, fmt.Errorf("%w: %s.%s is not the host of %s.%s or a parent of it, "+
				"so a browser would not keep the cookie", authn.ErrInvalidConfig, ConfigNamespace,
				cookieDomainItem, gateNamespace, gatePublicURLItem)
		}
		spec.domain = domain
	}

	switch {
	case gm.AllowInsecure:
		spec.name = insecureCookieName
	case spec.domain != "":
		spec.name = secureCookieName
		spec.secure = true
	default:
		spec.name = hostCookieName
		spec.secure = true
	}
	return spec, nil
}

// publicHost reads the public address: its host, lower-cased, and whether it is
// to be treated as https, which a loopback http address is, since a browser
// keeps a Secure cookie for it.
func publicHost(raw string) (host string, secureScheme bool, err error) {
	if raw == "" {
		return "", false, fmt.Errorf("%w: %s.%s is empty, and the session cookie is named and "+
			"scoped by the address the deployment is reached at", authn.ErrInvalidConfig,
			gateNamespace, gatePublicURLItem)
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, fmt.Errorf("%w: %s.%s is not an http or https URL with a host",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem)
	}
	host = strings.ToLower(u.Hostname())
	return host, u.Scheme == "https" || loopbackHost(host), nil
}

// loopbackHost reports whether host names this machine's loopback interface.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			cookieDomainItem: {
				Description: "when set, the Domain of the session cookie, which is then named __Secure-sid and " +
					"can be set by any subdomain; empty keeps the cookie to the host that set it",
			},
			sameSiteItem: {
				Description: "the SameSite attribute of the session cookie, lax or strict; none is refused",
			},
		},
	}
}

// readConfig decodes this module's section and the gateway's address and
// validates both. Prepare and New both go through it, so the two see one
// cookie.
func readConfig(reader config.Reader) (cookieSpec, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return cookieSpec{}, err
	}
	var gm gateMount
	if err := reader.Decode(gateNamespace+".mount", &gm); err != nil {
		return cookieSpec{}, err
	}
	return cfg.resolve(gm)
}
