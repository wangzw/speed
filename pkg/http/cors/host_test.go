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
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// waitLimit bounds every wait in these cases, so that a startup which never
// reaches Serve fails the case with a message instead of the whole binary at the
// global timeout.
func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

const guardedEndpoint = "public"

type host struct {
	addr  string
	chain string
}

// refusingGuardLayer is a stand-in for a layer that refuses a request, in the same stage as the CORS layer and leaving Order at zero, so ordering puts it inside CORS. It refuses every request, which lets a case show a refusal carries the CORS headers and a preflight never reaches it.
func refusingGuardLayer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  "stand-in.refuse",
		Stage: speedhttp.StageGuard,
		Handler: func(c *gin.Context) {
			speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusTooManyRequests})
		},
	}
}

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
	limit := waitLimit()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("the assembly did not stop cleanly: %v", runErr)
			}
		case <-time.After(limit):
			t.Errorf("the assembly did not stop within %s", limit)
		}
	})

	select {
	case <-served:
	case <-done:
		t.Fatalf("the assembly ended before it served: %v", runErr)
	case <-time.After(limit):
		t.Fatalf("the assembly did not reach Serve within %s", limit)
	}

	h.addr, h.chain = logs.listed()
	if h.addr == "" {
		t.Fatal("the entry point wrote no record of the address it bound")
	}
	return h
}

func loadReader(t *testing.T, corsPolicy string) config.Reader {
	t.Helper()
	document := fmt.Sprintf(
		`{"http": {"listeners": {"backend": {"address": "127.0.0.1:0"}}, `+
			`"endpoints": {%q: {"listener": "backend"}}}, "http.cors": {"endpoints": {%q: %s}}}`,
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
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: Module().Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

type response struct {
	status int
	header nethttp.Header
	body   string
}

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
		Timeout:   waitLimit(),
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

func (h *host) get(t *testing.T, path string) response {
	t.Helper()
	return h.send(t, nethttp.MethodGet, path, nil)
}

// TestAPreflightIsAnsweredBeforeARefusingLayer drives the property the layer's stage and position
// exist for: a preflight is answered by this layer before it reaches a layer that would refuse it.
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

// TestARefusalCarriesTheCORSHeaders drives the other half of the same property: a refusal from a
// layer inside CORS reaches the browser with the CORS headers.
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

// TestAnOriginOutsideThePolicyIsRefused checks that a request from an origin outside the policy is
// refused.
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

// TestAnOrdinaryRequestReachesTheRoute checks that a request without an Origin header reaches the
// route.
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

// TestAnAllowedOriginReachesTheRoute checks that a request from an allowed origin reaches the
// route.
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

// TestTheStarOriginAllowsEveryOrigin checks that a "*" policy admits every origin.
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

// TestTheLayerIsListedInTheGuardStageOutsideAnotherGuardLayer checks that the CORS layer registers
// in the guard stage outside another guard layer.
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

type entryRecorder struct {
	mu    sync.Mutex
	addr  string
	chain string
}

func (*entryRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *entryRecorder) Handle(_ context.Context, rec slog.Record) error {
	var key string
	switch rec.Message {
	case "an HTTP listener is listening":
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

type standInLogger struct{ logs *entryRecorder }

func (l standInLogger) Named(string) *slog.Logger { return slog.New(l.logs) }
func (standInLogger) Redaction() log.Redaction    { return noRedaction{} }

var _ log.Logger = standInLogger{}

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}
