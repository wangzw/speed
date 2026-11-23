package resolve

import (
	"net"
	nethttp "net/http"
	"strings"

	"github.com/vislake/speed/pkg/tenant"
)

// The three extractors this module registers. A host selects one by the name it
// is registered under; a module registering its own picks another name, because
// two registrations under one name are a startup failure.
var (
	_ tenant.Extractor = headerExtractor{}
	_ tenant.Extractor = subdomainExtractor{}
	_ tenant.Extractor = pathExtractor{}
)

// headerExtractor reads a named header.
//
// The value is trimmed of surrounding space: a header written by hand or by a
// proxy routinely carries it, and a name with a trailing space would fail to
// match the roster for no reason the operator could see.
type headerExtractor struct{ name string }

// Extract implements tenant.Extractor: it returns the named header's value,
// trimmed of surrounding space, and false when the header is absent or blank.
func (e headerExtractor) Extract(r *nethttp.Request) (string, bool) {
	name := strings.TrimSpace(r.Header.Get(e.name))
	return name, name != ""
}

// subdomainExtractor reads one dot-separated label of the host.
//
// A port is dropped before the labels are counted, so that
// acme.example.com:8443 reads the same as acme.example.com. Counting is from
// the left, which is the direction a reader of a hostname scans it; a
// deployment whose host carries a variable number of leading labels is better
// served by a path prefix, where the segments are the application's own.
//
// The label is handed over in lower case. A host name is case-insensitive, so
// ACME.example.com and acme.example.com are one host and have to reach one
// tenant; the roster matches a name byte for byte, and folding here is what
// makes the two spellings one name. A name served through this extractor is
// therefore registered in lower case.
type subdomainExtractor struct{ index int }

// Extract implements tenant.Extractor: it returns the host label at the
// configured index in lower case, and false when the host has no label there.
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

// pathExtractor reads one segment of the request path.
//
// Leading and trailing slashes are not segments, so /acme/orders and
// /acme/orders/ read the same at every index. The path is that of the request
// as it arrived; a proxy that strips a prefix before forwarding is the host's
// arrangement, and the configuration is written against what this process sees.
type pathExtractor struct{ index int }

// Extract implements tenant.Extractor: it returns the path segment at the
// configured index, and false when the path has no segment there.
func (e pathExtractor) Extract(r *nethttp.Request) (string, bool) {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if e.index < 0 || e.index >= len(segments) {
		return "", false
	}
	name := segments[e.index]
	return name, name != ""
}
