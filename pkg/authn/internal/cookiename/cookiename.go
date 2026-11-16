// Package cookiename names and builds the cookies the authn modules write, and
// decides whether they are Secure, from the scheme of the public URL the
// deployment is reached at (authn.gate.mount.public-url) and nothing else. It
// also holds the loopback host test the startup admission of an http public URL
// uses.
//
// On an https public URL, https://localhost included, a host-only cookie is
// __Host-<stem>, a cookie that carries a Domain is __Secure-<stem>, and every
// cookie is Secure. On an http public URL a cookie is <stem> and is not Secure,
// because WebKit refuses a __Host- or __Secure- cookie set over http even on a
// loopback host, and WebKit on Linux stores no Secure cookie set over http at
// all, loopback host included. Startup admits an http public URL only on a
// loopback host, whose traffic never leaves the machine and for which only a
// process on the same machine can set a cookie, so neither attribute protects
// anything where the rule leaves it off. Behind a proxy that terminates TLS the
// public URL is https, and the cookies are Secure whatever scheme the hop to the
// process uses. HttpOnly, Path=/ and the SameSite the writer chose are the same
// on both. A writer that offers a Domain refuses one on an http public URL,
// since a Domain cookie needs the __Secure- prefix and so Secure.
//
// Every net/http cookie the authn modules write, a set or a clear, is built by
// Cookie, which takes from the writer only what varies between cookies, and the
// session module's cookie library is configured from a cookie Cookie built. The
// name, Secure, HttpOnly and Path=/ are decided here and nowhere else.
package cookiename

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The stems of the cookies the authn modules write. The authn- part keeps a
// name that carries no prefix apart from a cookie another application on the
// same loopback host writes, which a browser sends to every port of the host.
const (
	// Session is the stem of the session cookie.
	Session = "authn-sid"
	// Flow is the stem of the cookie a login flow's binding travels in.
	Flow = "authn-flow"
	// Reset is the stem of the cookie an anonymous password reset's binding
	// travels in.
	Reset = "authn-reset"
)

// The prefixes a browser holds a cookie's attributes to. __Host- makes a cookie
// Secure, host-only and Path=/, so no subdomain can set it; __Secure- only
// requires Secure and leaves Domain free.
const (
	HostPrefix   = "__Host-"
	SecurePrefix = "__Secure-"
)

// Name is the name a cookie of the given stem is written and read under at
// publicURL. domain is the cookie's Domain attribute, empty for a host-only
// cookie. An http public URL gives the bare stem whatever domain is; a writer
// that cannot carry a Domain there refuses that configuration before it asks
// for a name.
func Name(publicURL, stem, domain string) string {
	switch {
	case HTTP(publicURL):
		return stem
	case domain != "":
		return SecurePrefix + stem
	default:
		return HostPrefix + stem
	}
}

// Secure reports whether a cookie written at publicURL carries the Secure
// attribute: every public URL but an http one, the same test that leaves Name
// without a prefix. Cookie sets Secure from it, on a set and on a clear.
func Secure(publicURL string) bool {
	return !HTTP(publicURL)
}

// Spec is what one writer decides about a cookie it writes: the stem, the
// value, the Domain, empty for a host-only cookie, the Max-Age, where a negative
// one clears the cookie and zero leaves it to the browser session, and the
// SameSite. A writer that cannot carry a Domain on an http public URL refuses
// that configuration before it builds a cookie.
type Spec struct {
	Stem     string
	Value    string
	Domain   string
	MaxAge   int
	SameSite http.SameSite
}

// Cookie builds the cookie s describes as it is written at publicURL: named by
// Name, Secure as Secure decides, HttpOnly, Path=/, and the writer's value,
// Domain, Max-Age and SameSite. A set and a clear of one cookie differ only in
// the value and the Max-Age, so a clear names the cookie it replaces.
func Cookie(publicURL string, s Spec) *http.Cookie {
	//nolint:gosec // G124: Secure comes from Secure, which is false only when
	// the public URL is http. Startup admits an http public URL only on a
	// loopback host, where a browser does not keep a Secure cookie on every
	// platform and the attribute protects nothing, because the traffic does not
	// leave the machine; on an https public URL it is true. HttpOnly is the
	// constant true, and every caller passes SameSite as the constant Lax or
	// Strict, or as the session module's configured value, which refuses None.
	return &http.Cookie{
		Name:     Name(publicURL, s.Stem, s.Domain),
		Value:    s.Value,
		Path:     "/",
		Domain:   s.Domain,
		MaxAge:   s.MaxAge,
		Secure:   Secure(publicURL),
		HttpOnly: true,
		SameSite: s.SameSite,
	}
}

// HTTP reports whether publicURL is an http URL. A value that does not parse,
// or carries another scheme, is not, so its cookies keep the prefix and Secure.
func HTTP(publicURL string) bool {
	u, err := url.Parse(publicURL)
	return err == nil && u.Scheme == "http"
}

// LoopbackHost reports whether host names this machine's loopback interface:
// the name localhost in any case, or a loopback IP address. The startup
// admission of an http public URL uses it; the cookie rule does not.
func LoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
