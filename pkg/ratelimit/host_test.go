package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

const guardedEndpoint = "public"

const anonymousHeader = "X-Anonymous"

type hostOptions struct {
	limiter        *limiter
	viaModule      bool
	responder      speedhttp.ErrorResponder
	routeStatus    int
	trustedProxies []string
	register       func(ep speedhttp.Endpoint)
}

type host struct {
	addr      string
	decisions *decisions
	reached   *counter
	chain     string
}

type decisions struct {
	mu   sync.Mutex
	list []decision
}

func (d *decisions) add(one decision) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.list = append(d.list, one)
}

func (d *decisions) all() []decision {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]decision(nil), d.list...)
}

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

func (c *counter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func startHost(t *testing.T, opts hostOptions) *host {
	t.Helper()
	checkCallers(t)
	l := opts.limiter
	h := &host{decisions: &decisions{}, reached: &counter{}}
	logs := &entryRecorder{}
	served := make(chan struct{})

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(loadReader(t, opts), config.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return standInLogger{logs: logs}, nil },
	})
	if opts.responder != nil {
		reg.Register(core.Module{
			Name:     "stand-in.responder",
			Provides: []core.Provision{{Token: (*speedhttp.ErrorResponder)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return opts.responder, nil },
		})
	}
	if opts.viaModule {
		reg.Register(Module())
		reg.Supply(l, ModuleName)
		reg.Register(core.Module{
			Name:     "stand-in.settings",
			Provides: []core.Provision{{Token: (*settings.Store)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return l.store, nil },
		})
		reg.Register(core.Module{
			Name:     "stand-in.counters",
			Provides: []core.Provision{{Token: (*kv.KV)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return l.counters, nil },
		})
	}
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
			ep.Use(stampingLayer(t))
			if !opts.viaModule {
				ep.Use(recordingLayer(l, h.decisions))
			}
			if opts.register != nil {
				opts.register(ep)
			}
			status := opts.routeStatus
			if status == 0 {
				status = nethttp.StatusNoContent
			}
			ep.Route(nethttp.MethodGet, "/things", func(c *gin.Context) {
				h.reached.inc()
				c.Status(status)
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

func recordingLayer(l *limiter, made *decisions) speedhttp.Middleware {
	mw := l.middleware()
	mw.Handler = func(c *gin.Context) {
		id, identified := authn.FromContext(c.Request.Context())
		d := l.decide(c, id, identified)
		l.signal.settle(d)
		made.add(d)
	}
	return mw
}

func loadReader(t *testing.T, opts hostOptions) config.Reader {
	t.Helper()
	endpoint := map[string]any{"listener": guardedEndpoint}
	if len(opts.trustedProxies) > 0 {
		endpoint["trusted-proxies"] = opts.trustedProxies
	}
	document, err := json.Marshal(map[string]any{
		"http": map[string]any{
			"listeners": map[string]any{guardedEndpoint: map[string]any{"address": "127.0.0.1:0"}},
			"endpoints": map[string]any{guardedEndpoint: endpoint},
		},
	})
	if err != nil {
		t.Fatalf("marshalling the config document: %v", err)
	}
	return loadDocument(t, document)
}

func loadDocument(t *testing.T, document []byte) config.Reader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "RATELIMITTEST", DefaultLocator: "file:" + path}},
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

func requireProblem(t *testing.T, got response, status int) {
	t.Helper()
	if got.status != status {
		t.Fatalf("the response is %d, want %d (body %q)", got.status, status, got.body)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("the response's media type is %q, want application/problem+json", ct)
	}
	var members map[string]any
	if err := json.Unmarshal([]byte(got.body), &members); err != nil {
		t.Fatalf("the response body %q is not a JSON object: %v", got.body, err)
	}
	want := map[string]any{
		"type":   "about:blank",
		"title":  nethttp.StatusText(status),
		"status": float64(status),
	}
	if len(members) != len(want) {
		t.Fatalf("the response body is %v, want exactly %v", members, want)
	}
	for k, v := range want {
		if members[k] != v {
			t.Fatalf("the response body is %v, want %s = %v", members, k, v)
		}
	}
}

func requireRetryAfter(t *testing.T, got response) int {
	t.Helper()
	raw := got.header.Get("Retry-After")
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 1 {
		t.Fatalf("Retry-After is %q, want a whole number of seconds, at least 1", raw)
	}
	return seconds
}

func requireThrottledProblem(t *testing.T, got response) {
	t.Helper()
	if got.status != nethttp.StatusTooManyRequests {
		t.Fatalf("the response is %d, want 429 (body %q)", got.status, got.body)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("the response's media type is %q, want application/problem+json", ct)
	}
	var members map[string]any
	if err := json.Unmarshal([]byte(got.body), &members); err != nil {
		t.Fatalf("the response body %q is not a JSON object: %v", got.body, err)
	}
	want := map[string]any{
		"type":   typePrefix + "throttled",
		"title":  nethttp.StatusText(nethttp.StatusTooManyRequests),
		"status": float64(nethttp.StatusTooManyRequests),
	}
	if len(members) != len(want) {
		t.Fatalf("the response body is %v, want exactly %v", members, want)
	}
	for k, v := range want {
		if members[k] != v {
			t.Fatalf("the response body is %v, want %s = %v", members, k, v)
		}
	}
}

type capturingResponder struct {
	mu       sync.Mutex
	problems []speedhttp.Problem
}

const customMediaType = "application/x.test-error+json"

func (r *capturingResponder) RespondError(w nethttp.ResponseWriter, _ *nethttp.Request, p speedhttp.Problem) {
	r.mu.Lock()
	r.problems = append(r.problems, p)
	r.mu.Unlock()
	w.Header().Set("Content-Type", customMediaType)
	w.WriteHeader(p.Status)
	_, _ = w.Write([]byte(`{"code":` + strconv.Itoa(p.Status) + `}`))
}

func (r *capturingResponder) handed() []speedhttp.Problem {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]speedhttp.Problem(nil), r.problems...)
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

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
