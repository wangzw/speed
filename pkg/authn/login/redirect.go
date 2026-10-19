package login

import (
	"net/url"
	"path"
	"strings"
)

// redirectTarget is the Location value a redirect answers with. A target that
// is a path of this server has its dot segments removed and a trailing slash
// kept, and every byte outside ASCII is percent-escaped: that is how net/http's
// Redirect writes the header, and a raw non-ASCII byte is not a valid URI
// reference in a header field. requestPath is the path of the request being
// answered, which a target that does not begin with "/" is resolved against.
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

// upperhex is the hexadecimal alphabet of a percent escape.
const upperhex = "0123456789ABCDEF"

// hexEscapeNonASCII percent-escapes every byte at or above 0x80.
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
