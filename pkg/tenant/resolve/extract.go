package resolve

import (
	"net"
	nethttp "net/http"
	"strings"

	"github.com/vislake/speed/pkg/tenant"
)

var (
	_ tenant.Extractor = headerExtractor{}
	_ tenant.Extractor = subdomainExtractor{}
	_ tenant.Extractor = pathExtractor{}
)

// headerExtractor trims surrounding space from the header value.
type headerExtractor struct{ name string }

// Extract implements tenant.Extractor.
func (e headerExtractor) Extract(r *nethttp.Request) (string, bool) {
	name := strings.TrimSpace(r.Header.Get(e.name))
	return name, name != ""
}

// subdomainExtractor drops a port before counting labels from the left and
// lower-cases the label, because the roster matches names byte for byte.
type subdomainExtractor struct{ index int }

// Extract implements tenant.Extractor.
func (e subdomainExtractor) Extract(r *nethttp.Request) (string, bool) {
	host := r.Host
	if withoutPort, _, err := net.SplitHostPort(host); err == nil {
		host = withoutPort
	}
	labels := strings.Split(host, ".")
	if e.index < 0 || e.index >= len(labels) {
		return "", false
	}
	name := strings.ToLower(labels[e.index])
	return name, name != ""
}

// pathExtractor ignores leading and trailing slashes and reads the path as the
// request arrived, not as a proxy may have rewritten it.
type pathExtractor struct{ index int }

// Extract implements tenant.Extractor.
func (e pathExtractor) Extract(r *nethttp.Request) (string, bool) {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if e.index < 0 || e.index >= len(segments) {
		return "", false
	}
	name := segments[e.index]
	return name, name != ""
}
