package login

import (
	"net/url"
	"path"
	"strings"
)

// redirectTarget mirrors net/http's Redirect: dot segments removed for a local path, a trailing slash kept, and every non-ASCII byte percent-escaped.
func redirectTarget(location, requestPath string) string {
	if u, err := url.Parse(location); err == nil && u.Scheme == "" && u.Host == "" {
		if requestPath == "" {
			requestPath = "/"
		}
		if location == "" || location[0] != '/' {
			dir, _ := path.Split(requestPath)
			location = dir + location
		}
		target, query, hasQuery := strings.Cut(location, "?")
		trailing := strings.HasSuffix(target, "/")
		target = path.Clean(target)
		if trailing && !strings.HasSuffix(target, "/") {
			target += "/"
		}
		if hasQuery {
			target += "?" + query
		}
		location = target
	}
	return hexEscapeNonASCII(location)
}

const upperhex = "0123456789ABCDEF"

func hexEscapeNonASCII(value string) string {
	escaped := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x80 {
			escaped = append(escaped, b)
			continue
		}
		escaped = append(escaped, '%', upperhex[b>>4], upperhex[b&0xf])
	}
	return string(escaped)
}
