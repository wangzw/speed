package session

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace, and the key of the
// gateway it reads. The gateway declares the public URL and this module reads
// it in Prepare, because whether the session cookie can be written at all, and
// the host its Domain has to cover, follow from the public address the
// deployment is reached at.
const (
	cookieDomainItem = "cookie-domain"
	sameSiteItem     = "same-site"

	gateNamespace     = "authn.gate"
	gatePublicURLItem = "mount.public-url"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// CookieDomain, when non-empty, is written as the Domain attribute of the
	// session cookie and makes the cookie __Secure-authn-sid, which loses the
	// guarantee that no subdomain can set it. An http public URL refuses it: a
	// cookie with a Domain needs the __Secure- prefix, and so Secure, which an
	// http public URL leaves off.
	CookieDomain string `config:"cookie-domain"`
	// SameSite is lax or strict. none is refused.
	SameSite string `config:"same-site"`
}

// configDefaults is what this module is worth without a configuration file.
var configDefaults = moduleConfig{SameSite: "lax"}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
}

// cookieSpec is what the configuration comes to: the public URL the session
// cookie is built for and the attributes of it that vary with the
// configuration, and the diagnostics that follow from them. cookiename.Cookie
// builds the cookie from it, so the name is the one cookiename.Name gives the
// public URL, __Host-authn-sid, __Secure-authn-sid with a Domain, and authn-sid
// on an http public URL, and Secure is what cookiename.Secure gives the same
// URL, on https and off on http. HttpOnly and Path=/ do not vary.
type cookieSpec struct {
	publicURL string
	domain    string
	sameSite  http.SameSite
}

// cookie builds the session cookie with the given value and Max-Age.
func (s cookieSpec) cookie(value string, maxAge int) *http.Cookie {
	return cookiename.Cookie(s.publicURL, cookiename.Spec{
		Stem:     cookiename.Session,
		Value:    value,
		Domain:   s.domain,
		MaxAge:   maxAge,
		SameSite: s.sameSite,
	})
}

// name is the name the session cookie is written and read under, the one
// cookiename.Cookie gives it.
func (s cookieSpec) name() string {
	return cookiename.Name(s.publicURL, cookiename.Session, s.domain)
}

// domainShape is a host name as a cookie's Domain attribute may carry it:
// letters, digits and hyphens in labels separated by dots.
var domainShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// resolve turns decoded configuration into the cookie this module writes,
// refusing what cannot be run on.
func (c moduleConfig) resolve(gm gateMount) (cookieSpec, error) {
	var spec cookieSpec

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

	host, admitted, plainHTTP, err := publicHost(gm.PublicURL)
	if err != nil {
		return cookieSpec{}, err
	}
	if !admitted {
		return cookieSpec{}, fmt.Errorf("%w: %s.%s is http on a host that is not a loopback one, so the "+
			"session cookie would cross the network in the clear. Use https",
			authn.ErrInsecureConfig, gateNamespace, gatePublicURLItem)
	}

	if c.CookieDomain != "" {
		if plainHTTP {
			return cookieSpec{}, fmt.Errorf("%w: %s.%s is set and %s.%s is http: a cookie with a Domain "+
				"needs the __Secure- prefix and so Secure, which an http public URL leaves off. Leave %s.%s empty",
				authn.ErrInvalidConfig, ConfigNamespace, cookieDomainItem, gateNamespace, gatePublicURLItem,
				ConfigNamespace, cookieDomainItem)
		}
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

	spec.publicURL = gm.PublicURL
	return spec, nil
}

// publicHost reads the public address: its host, lower-cased, whether startup
// admits it, which it does for https and for http on a loopback host, whose
// traffic never leaves the machine, and whether it is http, which writes the
// session cookie without a prefix and without Secure.
func publicHost(raw string) (host string, admitted, plainHTTP bool, err error) {
	if raw == "" {
		return "", false, false, fmt.Errorf("%w: %s.%s is empty, and the session cookie is named and "+
			"scoped by the address the deployment is reached at", authn.ErrInvalidConfig,
			gateNamespace, gatePublicURLItem)
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, false, fmt.Errorf("%w: %s.%s is not an http or https URL with a host",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem)
	}
	host = strings.ToLower(u.Hostname())
	plainHTTP = cookiename.HTTP(raw)
	return host, !plainHTTP || cookiename.LoopbackHost(u.Hostname()), plainHTTP, nil
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			cookieDomainItem: {
				Description: "when set, the Domain of the session cookie, which is then named __Secure-authn-sid " +
					"and can be set by any subdomain; empty keeps the cookie to the host that set it. An " +
					"http public URL refuses it",
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
