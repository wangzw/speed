package mount

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Origin is the scheme://host[:port] of publicURL in the form a browser sends it
// in an Origin request header, which the cross-origin check compares byte for
// byte: the scheme and host are lower-case, the port is left out when it is the
// scheme's default, and a trailing dot on the host is dropped. An empty
// publicURL has no origin and is not an error.
//
// What cannot be turned into an origin is refused, because an origin built from
// it would trust nothing or the wrong thing: a value that is not a URL with an
// http or https scheme and a host, one that carries user information, a query
// or a fragment, an empty or out-of-range port, a wildcard, and a host that is
// not ASCII (an internationalised name is written in punycode, which is what a
// browser sends).
func Origin(publicURL string) (string, error) {
	if publicURL == "" {
		return "", nil
	}
	return canonicalOrigin(publicURL, true)
}

func trustedOrigin(entry string) (string, error) {
	return canonicalOrigin(entry, false)
}

func canonicalOrigin(raw string, allowPath bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("the origin is not a URL with an http or https scheme and a host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", errors.New("the origin carries user information, a query or a fragment")
	}
	if !allowPath && u.Path != "" {
		return "", errors.New("the origin carries a path")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("the origin has an empty port")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.ContainsAny(host, "*%") || slices.ContainsFunc([]rune(host), func(r rune) bool { return r > unicode.MaxASCII }) {
		return "", errors.New("the origin's host is empty, a wildcard, a zoned address or not ASCII")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := u.Scheme + "://" + host
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("the origin's port is not between 1 and 65535")
		}
		if n != defaultPort[u.Scheme] {
			origin += ":" + strconv.Itoa(n)
		}
	}
	return origin, nil
}

var defaultPort = map[string]int{"http": 80, "https": 443}

// TrustedOrigins is every origin the cross-origin check trusts: the origin of
// publicURL first, when there is one, then each of trusted that is not already
// in the list. Every origin is in the canonical form of [Origin], so a list
// entry written as https://Example.com:443 is the origin a browser sends as
// https://example.com. An entry that is not an origin is refused and not
// skipped: a skipped entry would read as a list that was applied. The gateway's
// check on a cookie or a Basic credential and the check the Mounter applies to the routes
// of this capability are both built from it, so the two cannot come to trust
// different sets from the same configuration.
func TrustedOrigins(publicURL string, trusted []string) ([]string, error) {
	origin, err := Origin(publicURL)
	if err != nil {
		return nil, err
	}
	origins := make([]string, 0, len(trusted)+1)
	if origin != "" {
		origins = append(origins, origin)
	}
	for i, t := range trusted {
		canonical, err := trustedOrigin(t)
		if err != nil {
			// The entry is named by its position and not quoted: a value
			// written in the wrong form may carry user information.
			return nil, fmt.Errorf("entry %d of the trusted origins is not an origin in the form scheme://host[:port]: %w", i+1, err)
		}
		if !slices.Contains(origins, canonical) {
			origins = append(origins, canonical)
		}
	}
	return origins, nil
}

// CrossOrigin builds a cross-origin check that trusts TrustedOrigins(publicURL,
// trusted) and nothing else. An origin the standard library cannot trust, which
// is any that is not scheme://host[:port], is refused and not skipped: a
// skipped entry would read as a list that was applied.
//
// Every caller gets an instance of its own. The check has no state beyond its
// trusted set and its bypass patterns, and a pattern one holder adds is not one
// the other should honour.
func CrossOrigin(publicURL string, trusted []string) (*http.CrossOriginProtection, error) {
	origins, err := TrustedOrigins(publicURL, trusted)
	if err != nil {
		return nil, err
	}
	check := http.NewCrossOriginProtection()
	for _, origin := range origins {
		if err := check.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("the cross-origin check cannot trust one of the origins: %w", err)
		}
	}
	return check, nil
}
