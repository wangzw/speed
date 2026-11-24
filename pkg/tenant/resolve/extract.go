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

type headerExtractor struct{ name string }

func (e headerExtractor) Extract(r *nethttp.Request) (string, bool) {
	name := strings.TrimSpace(r.Header.Get(e.name))
	return name, name != ""
}

type subdomainExtractor struct{ index int }

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

type pathExtractor struct{ index int }

func (e pathExtractor) Extract(r *nethttp.Request) (string, bool) {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if e.index < 0 || e.index >= len(segments) {
		return "", false
	}
	name := segments[e.index]
	return name, name != ""
}
