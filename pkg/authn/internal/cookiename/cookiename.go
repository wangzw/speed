// Package cookiename names and builds the cookies the authn modules write. On an
// https public URL a cookie is __Host-<stem> when host-only or __Secure-<stem>
// with a Domain, and Secure; on an http one it is the bare stem and not Secure.
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

// Name is the name a cookie of stem is written and read under at publicURL:
// __Host-<stem> for a host-only cookie on https, __Secure-<stem> with a Domain,
// and the bare stem on http, where a Domain cookie is refused.
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

// Spec is what one writer decides about a cookie: the stem, value, Domain, the
// Max-Age (negative clears, zero ends with the browser session) and SameSite. A
// Domain on an http public URL is refused before a cookie is built.
type Spec struct {
	// Stem is the cookie's unprefixed name.
	Stem string
	// Value is the cookie's value.
	Value string
	// Domain is the cookie's Domain attribute; empty for a host-only cookie.
	Domain string
	// MaxAge is the cookie's Max-Age in seconds.
	MaxAge int
	// SameSite is the writer's SameSite attribute.
	SameSite http.SameSite
}

// Cookie builds the cookie s describes as written at publicURL: named by Name,
// HttpOnly, Path=/, Secure as Secure decides, with the writer's value, Domain,
// Max-Age and SameSite. A clear names the same cookie with a negative MaxAge.
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
