package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// stubGateway stands in for the authentication gateway on a guarded endpoint:
// one exempt route passes, and every other request — matched route, no match, or
// a method the path does not allow — is refused with one 401 carrying a fixed
// Vary and WWW-Authenticate.
//
// The two behaviours the differential below reads are the gateway's own: the
// exemption is looked up by (method, c.FullPath()), and every other request is
// refused before the endpoint's router or a route handler is reached. A stub
// that distinguished paths and methods beyond the exemption, or that let the
// endpoint answer, would make the comparison say nothing about dispatch. Its
// refusal shape mirrors pkg/authn/gate's.
func stubGateway(c *gin.Context) {
	if c.Request.Method == nethttp.MethodGet && c.FullPath() == "/m/public" {
		return
	}
	c.Header("Vary", "Authorization, Cookie")
	c.Header("WWW-Authenticate", `Bearer realm="guarded"`)
	Fail(c, Problem{Status: nethttp.StatusUnauthorized})
}

// Topologies: one listener carrying a fallback endpoint that claims "/", and one
// carrying no fallback at all.
const (
	topologyWithFallback    = "with a fallback"
	topologyWithoutFallback = "without a fallback"
)

// differentialAssembly is one assembly the differential compares: the routes the
// guarded endpoint registered, and nothing else. The gateway stub, the endpoints
// and their claims are the same across the comparison, so the only thing that
// changes between two runs is which routes exist.
type differentialAssembly struct {
	name   string
	routes [][2]string
}

// differentialPaths and differentialMethods are the request matrix. The paths
// cover the claimed subtree at its root, its root with a trailing slash, a
// registered route and a parameter beneath it, an unregistered path under the
// claim, a deep registered and a deep unregistered path, the exempt route, a
// path outside every claim, the sibling endpoint's subtree, and a path that
// differs from the claim only in case.
var (
	differentialPaths = []string{
		"/m", "/m/", "/m/items", "/m/items/1", "/m/zz", "/m/deep/x/y", "/m/deep/zz",
		"/m/public", "/zz", "/o/zz", "/M/items",
	}
	differentialMethods = []string{
		nethttp.MethodGet, nethttp.MethodHead, nethttp.MethodPost,
		nethttp.MethodDelete, nethttp.MethodOptions,
	}
)

// differentialCredentials are the two ways an unauthenticated caller arrives:
// with no credential, and with a Bearer header no backend takes. Both are
// unauthenticated, so both must be answered by the gateway the same way.
var differentialCredentials = []struct {
	name   string
	header nethttp.Header
}{
	{"no credential", nil},
	{"Bearer zz", nethttp.Header{"Authorization": {"Bearer zz"}}},
}

// replyFingerprint is one response without its Date header: the status, every
// other header (same-name values in the order the server wrote them) and the
// body. Date is the one header outside the comparison, because two runs started
// at different moments legitimately differ there.
type replyFingerprint struct {
	status  int
	headers string
	body    string
}

func (f replyFingerprint) String() string {
	return fmt.Sprintf("status %d, headers {%s}, body %q", f.status, f.headers, f.body)
}

// fingerprintOf renders what a client got back for comparison.
func fingerprintOf(a answer) replyFingerprint {
	names := make([]string, 0, len(a.header))
	for name := range a.header {
		if nethttp.CanonicalHeaderKey(name) == "Date" {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+": "+strings.Join(a.header[name], "\x00"))
	}
	return replyFingerprint{status: a.status, headers: strings.Join(lines, "\n"), body: a.body}
}

// probeAssembly starts one assembly for a topology and returns the answer to
// every request in the matrix.
func probeAssembly(t *testing.T, topology string, v differentialAssembly) (map[string]replyFingerprint, error) {
	t.Helper()

	var cfg moduleConfig
	steps := []func(*core.Registry) error{
		useLayer("guarded", Middleware{Name: "stub.gate", Stage: StageIdentify, Handler: stubGateway}),
	}
	switch topology {
	case topologyWithFallback:
		cfg = claimedConfig(map[string][]string{"guarded": {"/m"}, "web": {"/"}})
		steps = append(steps,
			routeOn("web", nethttp.MethodGet, "/*path", func(c *gin.Context) {
				c.String(nethttp.StatusOK, "web")
			}),
			routeOn("web", nethttp.MethodHead, "/*path", func(c *gin.Context) {
				c.Status(nethttp.StatusOK)
			}),
		)
	case topologyWithoutFallback:
		cfg = claimedConfig(map[string][]string{"guarded": {"/m"}, "other": {"/o"}})
		steps = append(steps, routeOn("other", nethttp.MethodGet, "/o/x", func(c *gin.Context) {
			c.String(nethttp.StatusOK, "other")
		}))
	default:
		return nil, fmt.Errorf("unknown topology %q", topology)
	}
	for _, r := range v.routes {
		steps = append(steps, routeOn("guarded", r[0], r[1], func(c *gin.Context) {
			c.String(nethttp.StatusOK, "handler")
		}))
	}

	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.claims-differential",
		requires: requiresRouter(),
		onInit:   inOrder(steps...),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t, cfg, Module(), probe.module())
	select {
	case <-serving:
	case err := <-a.result:
		a.mu.Lock()
		a.finished, a.err = true, err
		a.mu.Unlock()
		return nil, err
	}
	r, err := routerFrom(a.reg)
	if err != nil {
		return nil, err
	}
	addr, err := boundAddr(r, "guarded")
	if err != nil {
		return nil, err
	}

	out := make(map[string]replyFingerprint, len(differentialMethods)*len(differentialPaths)*len(differentialCredentials))
	for _, method := range differentialMethods {
		for _, path := range differentialPaths {
			for _, cred := range differentialCredentials {
				got, err := exchange(context.Background(), method, addr, path, cred.header)
				if err != nil {
					return nil, fmt.Errorf("%s %s with %s: %w", method, path, cred.name, err)
				}
				out[method+" "+path+" ("+cred.name+")"] = fingerprintOf(got)
			}
		}
	}
	return out, nil
}

// TestAnUnauthenticatedAnswerDoesNotDependOnTheAssembledRoutes is the mechanism
// differential for the dispatch half of the promise: on a guarded endpoint, an
// unauthenticated request is answered by the gateway before the endpoint's
// router would answer, so its answer must not change when routes are added to or
// removed from that endpoint's assembly.
//
// Three assemblies are compared on one topology at a time: all routes, no
// routes, and one deep route. The endpoint keeps the same claim in every run, so
// the dispatch selects the same endpoint for the same path throughout; only the
// endpoint's route table changes. The answer to every one of the matrix's
// (method × path × credential) combinations must be byte-identical — status,
// every header but Date, and the body — except the exemption GET /m/public,
// which is the one request an empty route table genuinely cannot serve.
//
// The exemption is also the non-hollowness check: if removing the routes changed
// no answer at all, the comparison would be proving nothing, so each variant has
// to differ there.
func TestAnUnauthenticatedAnswerDoesNotDependOnTheAssembledRoutes(t *testing.T) {
	all := differentialAssembly{
		name: "all routes",
		routes: [][2]string{
			{nethttp.MethodGet, "/m/items"},
			{nethttp.MethodPost, "/m/items/:id"},
			{nethttp.MethodGet, "/m/public"},
			{nethttp.MethodGet, "/m/deep/x/y"},
		},
	}
	variants := []differentialAssembly{
		{name: "no routes"},
		{name: "one deep route", routes: [][2]string{{nethttp.MethodGet, "/m/deep/x/y"}}},
	}

	// exemptRequest reports whether the combination is the one request the
	// comparison allows to differ: the gateway passes GET /m/public whatever the
	// caller carries, so the route table decides what it answers.
	exemptRequest := func(key string) bool { return strings.HasPrefix(key, "GET /m/public (") }

	for _, topology := range []string{topologyWithFallback, topologyWithoutFallback} {
		for _, variant := range variants {
			t.Run(topology+"/"+variant.name, func(t *testing.T) {
				base, err := probeAssembly(t, topology, all)
				if err != nil {
					t.Fatalf("the assembly with %s did not start: %v", all.name, err)
				}
				got, err := probeAssembly(t, topology, variant)
				if err != nil {
					t.Fatalf("the assembly with %s did not start: %v", variant.name, err)
				}

				keys := make([]string, 0, len(base))
				for key := range base {
					keys = append(keys, key)
				}
				slices.Sort(keys)

				exempt := 0
				for _, key := range keys {
					if base[key] == got[key] {
						continue
					}
					if exemptRequest(key) {
						exempt++
						t.Logf("the exempt request changed, as it must: %s\n  %s: %s\n  %s: %s",
							key, all.name, base[key], variant.name, got[key])
						continue
					}
					t.Errorf("the answer to %s changed with the assembled routes; an unauthenticated "+
						"caller must not be able to tell them apart:\n  %s: %s\n  %s: %s",
						key, all.name, base[key], variant.name, got[key])
				}
				if exempt == 0 {
					t.Errorf("removing routes changed no answer at all, the exempt GET /m/public "+
						"included: the %s assembly is indistinguishable from %s, so this comparison "+
						"holds nothing", variant.name, all.name)
				}
			})
		}
	}
}
