package cors

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The cases that drive the layer through a request stand on a real assembly: the
// entry point module itself, listening on a port the operating system picks, with
// this module registered into it. What the layer answers is read off a socket, so
// the routing, the shape of an error response and the headers that survive it are
// the entry point's and not a stand-in's, and the position the layer claims is
// exercised through the entry point's own ordering.

// waitLimit bounds every wait in these cases, so that a startup which never
// reaches Serve fails the case with a message instead of the whole binary at the
// global timeout.
const waitLimit = 10 * time.Second

// guardedEndpoint is the one endpoint every assembly declares and the layer
// guards.
const guardedEndpoint = "public"

// host is one assembly that is serving.
type host struct {
	addr string
	// chain is the entry point's listing of the layers on the guarded endpoint,
	// in the order they are assembled.
	chain string
}

// refusingGuardLayer is a stand-in for a layer that refuses a request, in the
// same stage as the CORS layer and at its default position, so the ordering puts
// it inside CORS. It refuses every request it sees, which is what lets a case
// show that a refusal carries the CORS headers and that a preflight never reaches
// it.
func refusingGuardLayer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  "stand-in.refuse",
		Stage: speedhttp.StageGuard,
		Handler: func(c *gin.Context) {
			speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusTooManyRequests})
		},
	}
}

// startHost builds an assembly on the given endpoint policy and runs it until the
// entry point is serving. register, when set, runs in the registrant's Init with
// the guarded endpoint, for the layers a case puts beside this module's. The run
// is ended when the case is, and a run that ends with an error fails the case.
func startHost(t *testing.T, corsPolicy string, register func(speedhttp.Endpoint)) *host {
	t.Helper()
	h := &host{}
	logs := &entryRecorder{}
	served := make(chan struct{})

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(loadReader(t, corsPolicy), config.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return standInLogger{logs: logs}, nil },
	})
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "registrant",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ep, err := router.Endpoint(guardedEndpoint)
			if err != nil {
				return err
			}
			if register != nil {
				register(ep)
			}
			ep.Route(nethttp.MethodGet, "/things", func(c *gin.Context) {
				c.Status(nethttp.StatusNoContent)
			})
			return nil
		},
		Serve: func(context.Context, *core.Registry, any) error {
			close(served)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = reg.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("the assembly did not stop cleanly: %v", runErr)
			}
		case <-time.After(waitLimit):
			t.Errorf("the assembly did not stop within %s", waitLimit)
		}
	})

	select {
	case <-served:
	case <-done:
		t.Fatalf("the assembly ended before it served: %v", runErr)
	case <-time.After(waitLimit):
		t.Fatalf("the assembly did not reach Serve within %s", waitLimit)
	}

	h.addr, h.chain = logs.listed()
	if h.addr == "" {
		t.Fatal("the entry point wrote no record of the address it bound")
	}
	return h
}

// loadReader loads the configuration this assembly runs on through the real
// loader: the entry point declares one endpoint on a port of the system's
// choosing, and the CORS module carries the given policy for that endpoint.
func loadReader(t *testing.T, corsPolicy string) config.Reader {
	t.Helper()
	document := fmt.Sprintf(
		`{"http": {"endpoints": {%q: {"address": "127.0.0.1:0"}}}, "http.cors": {"endpoints": {%q: %s}}}`,
		guardedEndpoint, guardedEndpoint, corsPolicy)

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "CORSTEST", DefaultLocator: "file:" + path}},
	})
	// The declarations reach the load as the resources of registered modules,
	// which is the only way one ever reaches it.
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: Module().Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// response is what a client got back.
type response struct {
	status int
	header nethttp.Header
	body   string
}

// send issues one request over a connection of its own and reads the response to
// its end.
func (h *host) send(t *testing.T, method, path string, header map[string]string) response {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, "http://"+h.addr+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := &nethttp.Client{
		Timeout:   waitLimit,
		Transport: &nethttp.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response to %s %s: %v", method, path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// get is send for a GET without extra headers.
func (h *host) get(t *testing.T, path string) response {
	t.Helper()
	return h.send(t, nethttp.MethodGet, path, nil)
}

// TestAPreflightIsAnsweredBeforeARefusingLayer drives the property the layer's
// stage and position exist for: a preflight is answered by this layer before it
// reaches a layer that would refuse it. The path carries a GET route only, so the
// OPTIONS request is one the entry point would answer 405 — the preflight the
// entry point never routed is exactly the one a browser sends.
func TestAPreflightIsAnsweredBeforeARefusingLayer(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["https://app.example"]}`, func(ep speedhttp.Endpoint) {
		ep.Use(refusingGuardLayer())
	})
	got := h.send(t, nethttp.MethodOptions, "/things", map[string]string{
		"Origin":                        "https://app.example",
		"Access-Control-Request-Method": "GET",
	})
	if got.status != nethttp.StatusNoContent {
		t.Fatalf("the preflight was answered %d, want 204: the CORS layer did not run outside the "+
			"refusing layer (body %q)", got.status, got.body)
	}
	if got.header.Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("the preflight carries no Access-Control-Allow-Methods: %v", got.header)
	}
	if origin := got.header.Get("Access-Control-Allow-Origin"); origin != "https://app.example" {
		t.Fatalf("the preflight carries Access-Control-Allow-Origin %q, want the request origin",
			origin)
	}
}

// TestARefusalCarriesTheCORSHeaders drives the other half of the same property: a
// refusal from a layer inside CORS reaches the browser with the CORS headers, so
// the browser reports the status rather than a network error.
func TestARefusalCarriesTheCORSHeaders(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["https://app.example"]}`, func(ep speedhttp.Endpoint) {
		ep.Use(refusingGuardLayer())
	})
	got := h.send(t, nethttp.MethodGet, "/things", map[string]string{"Origin": "https://app.example"})
	if got.status != nethttp.StatusTooManyRequests {
		t.Fatalf("the request was answered %d, want 429 from the refusing layer (body %q)",
			got.status, got.body)
	}
	if origin := got.header.Get("Access-Control-Allow-Origin"); origin != "https://app.example" {
		t.Fatalf("the refusal carries Access-Control-Allow-Origin %q, want the request origin: "+
			"the CORS layer did not run outside the refusing layer", origin)
	}
}

func TestAnOriginOutsideThePolicyIsRefused(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["https://app.example"]}`, nil)
	got := h.send(t, nethttp.MethodGet, "/things", map[string]string{"Origin": "https://evil.example"})
	if got.status != nethttp.StatusForbidden {
		t.Fatalf("a request from a disallowed origin was answered %d, want 403 (body %q)",
			got.status, got.body)
	}
	if got.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a refusal for a disallowed origin carries Access-Control-Allow-Origin: %v",
			got.header)
	}
}

func TestAnOrdinaryRequestReachesTheRoute(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["https://app.example"]}`, nil)
	got := h.get(t, "/things")
	if got.status != nethttp.StatusNoContent {
		t.Fatalf("a request with no Origin was answered %d, want 204 from the route (body %q)",
			got.status, got.body)
	}
	if got.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a request with no Origin carries Access-Control-Allow-Origin: %v", got.header)
	}
}

func TestAnAllowedOriginReachesTheRoute(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["https://app.example"]}`, nil)
	got := h.send(t, nethttp.MethodGet, "/things", map[string]string{"Origin": "https://app.example"})
	if got.status != nethttp.StatusNoContent {
		t.Fatalf("a request from an allowed origin was answered %d, want 204 from the route (body %q)",
			got.status, got.body)
	}
	if origin := got.header.Get("Access-Control-Allow-Origin"); origin != "https://app.example" {
		t.Fatalf("the response carries Access-Control-Allow-Origin %q, want the request origin", origin)
	}
}

func TestTheStarOriginAllowsEveryOrigin(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["*"]}`, nil)
	got := h.send(t, nethttp.MethodGet, "/things", map[string]string{"Origin": "https://anywhere.example"})
	if got.status != nethttp.StatusNoContent {
		t.Fatalf("a request with an unrelated origin was answered %d, want 204 (body %q)",
			got.status, got.body)
	}
	if origin := got.header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("the response carries Access-Control-Allow-Origin %q, want \"*\"", origin)
	}
}

func TestTheLayerIsListedInTheGuardStageOutsideAnotherGuardLayer(t *testing.T) {
	h := startHost(t, `{"allow-origins": ["https://app.example"]}`, func(ep speedhttp.Endpoint) {
		ep.Use(refusingGuardLayer())
	})
	if !strings.Contains(h.chain, "[guard] http.cors") {
		t.Fatalf("the chain %q does not mark http.cors as the guard stage's first layer", h.chain)
	}
	cors := strings.Index(h.chain, "http.cors")
	other := strings.Index(h.chain, "stand-in.refuse")
	if cors < 0 || other < 0 {
		t.Fatalf("the chain %q does not name both layers", h.chain)
	}
	if cors > other {
		t.Fatalf("the chain %q places http.cors inside the stand-in guard layer", h.chain)
	}
}

// entryRecorder is a slog handler that keeps two things of what the entry point
// writes: the address of its bound socket, which is the only way a client outside
// the process learns the port the system picked, and the listing of the layers on
// its chain.
type entryRecorder struct {
	mu    sync.Mutex
	addr  string
	chain string
}

func (*entryRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *entryRecorder) Handle(_ context.Context, rec slog.Record) error {
	var key string
	switch rec.Message {
	case "an HTTP endpoint is listening":
		key = "address"
	case "an HTTP endpoint's chain is assembled":
		key = "chain"
	default:
		return nil
	}
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key != key {
			return true
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if key == "address" {
			r.addr = a.Value.String()
		} else {
			r.chain = a.Value.String()
		}
		return true
	})
	return nil
}

func (r *entryRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *entryRecorder) WithGroup(string) slog.Handler      { return r }

func (r *entryRecorder) listed() (addr, chain string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.addr, r.chain
}

// standInLogger delivers the Logger capability over an entryRecorder.
type standInLogger struct{ logs *entryRecorder }

func (l standInLogger) Named(string) *slog.Logger { return slog.New(l.logs) }
func (standInLogger) Redaction() log.Redaction    { return noRedaction{} }

var _ log.Logger = standInLogger{}

// noRedaction takes registrations and masks nothing: what these cases observe of
// the Logger capability is what the entry point reports of itself.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}
