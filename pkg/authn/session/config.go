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

// The keys this module accepts, relative to its namespace, and the gateway's
// public URL key it reads in Prepare, because whether the session cookie can be
// written and its Domain follow from the public address.
const (
	cookieDomainItem = "cookie-domain"
	sameSiteItem     = "same-site"

	gateNamespace     = "authn.gate"
	gatePublicURLItem = "mount.public-url"
)

type moduleConfig struct {
	// CookieDomain, when non-empty, is the Domain of the session cookie and makes
	// it __Secure-authn-sid, which loses the guarantee no subdomain can set it. An
	// http public URL refuses it, as a Domain needs the __Secure- prefix and so Secure.
	CookieDomain string `config:"cookie-domain"`
	// SameSite is lax or strict. none is refused.
	SameSite string `config:"same-site"`
}

var configDefaults = moduleConfig{SameSite: "lax"}

type gateMount struct {
	PublicURL string `config:"public-url"`
}

// cookieSpec is what the configuration comes to: the public URL the session
// cookie is built for and the attributes that vary with the configuration.
// cookiename.Cookie builds it, so the name and Secure follow from that URL.
type cookieSpec struct {
	publicURL string
	domain    string
	sameSite  http.SameSite
}

func (s cookieSpec) cookie(value string, maxAge int) *http.Cookie {
	return cookiename.Cookie(s.publicURL, cookiename.Spec{
		Stem:     cookiename.Session,
		Value:    value,
		Domain:   s.domain,
		MaxAge:   maxAge,
		SameSite: s.sameSite,
	})
}

func (s cookieSpec) name() string {
	return cookiename.Name(s.publicURL, cookiename.Session, s.domain)
}

var domainShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

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

// publicHost reads the public address's host, whether startup admits it (https,
// or http on a loopback host) and whether it is http, which writes the session
// cookie without a prefix and without Secure.
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
