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

// Origin canonicalises publicURL to the scheme://host[:port] a browser sends in an
// Origin header: lower-case scheme and host, default port omitted, trailing dot
// dropped. An empty publicURL returns "", nil; anything else is refused.
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

// TrustedOrigins is every origin the cross-origin check trusts: publicURL's origin
// first when there is one, then each trusted entry not already present, all in
// [Origin]'s canonical form. An entry that is not an origin is refused, not skipped.
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

// CrossOrigin builds a check that trusts TrustedOrigins(publicURL, trusted) and
// nothing else, refusing an unusable entry rather than skipping it. Every caller
// gets an instance of its own.
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
