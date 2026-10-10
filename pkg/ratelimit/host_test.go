package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// The cases that drive the limiting layer through a request stand on a real
// assembly: the entry point module itself, listening on a port the operating
// system picks. What the layer answers is read off a socket, so the routing, the
// shape of an error response and the headers that survive it are the entry
// point's and not a stand-in's. The settings store and the counters are the
// case's own, the same stand-ins the decision's cases use.
//
// Reading the identity is the one step the layer takes that a case cannot drive:
// the identity is written only inside the authentication capability directory.
// A case therefore reaches the layer in one of two ways. Through the module's own
// descriptor, with the product supplied to it, the layer registered is the real
// one and a request carries no identity, which is the path an exempt route or a
// command without authentication takes. Otherwise the case registers the layer
// with the metadata this module declares for it, in front of a handler that fixes
// the identity — one zero identity for every request, unless the request carries
// the header named by anonymousHeader — and runs the same decision.

// waitLimit bounds every wait in these cases, so that a startup which never
// reaches Serve fails the case with a message instead of the whole binary at the
// global timeout.
const waitLimit = 10 * time.Second

// guardedEndpoint is the one endpoint every assembly declares and the layer
// guards.
const guardedEndpoint = "public"

// anonymousHeader marks a request the fixed-identity handler runs without an
// identity.
const anonymousHeader = "X-Anonymous"

// hostOptions is what one assembly is built from.
type hostOptions struct {
	// limiter is the product whose layer the assembly registers. Its settings
	// store, counters, signal and clock are the case's.
	limiter *limiter
	// viaModule registers the layer through this module's descriptor, with the
	// product supplied to it, instead of registering it with a fixed identity.
	viaModule bool
	// responder, when set, is delivered as the ErrorResponder capability, so
	// that it decides the shape of every error response.
	responder speedhttp.ErrorResponder
	// register, when set, runs in the registrant's Init with the guarded
	// endpoint, for the layers a case puts beside this module's.
	register func(ep speedhttp.Endpoint)
}

// host is one assembly that is serving.
type host struct {
	addr string
	// decisions are what the layer's decisions came to, in order. They are
	// kept only when the case registered the layer itself.
	decisions *decisions
	// reached counts the requests the route's handler served.
	reached *counter
	// chain is the entry point's listing of the layers on the guarded
	// endpoint, in the order they are assembled.
	chain string
}

// decisions is the list of decisions the fixed-identity handler made.
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

// counter counts events from the goroutines that serve requests.
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

// startHost builds an assembly from opts and runs it until the entry point is
// serving. The run is ended when the case is, and a run that ends with an error
// fails the case.
func startHost(t *testing.T, opts hostOptions) *host {
	t.Helper()
	l := opts.limiter
	h := &host{decisions: &decisions{}, reached: &counter{}}
	logs := &entryRecorder{}
	served := make(chan struct{})

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(loadReader(t), config.ModuleName)
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
			if !opts.viaModule {
				ep.Use(fixedIdentityLayer(l, h.decisions))
			}
			if opts.register != nil {
				opts.register(ep)
			}
			ep.Route(nethttp.MethodGet, "/things", func(c *gin.Context) {
				h.reached.inc()
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

// fixedIdentityLayer is the layer this module registers, with the metadata it
// declares for it, in front of a handler that fixes the identity and runs the
// decision and its accounting the way the real handler does.
func fixedIdentityLayer(l *limiter, made *decisions) speedhttp.Middleware {
	mw := l.middleware()
	mw.Handler = func(c *gin.Context) {
		_, anonymous := c.Request.Header[anonymousHeader]
		d := l.decide(c, authn.Identity{}, !anonymous)
		l.signal.settle(d)
		made.add(d)
	}
	return mw
}

// loadReader loads the configuration this assembly runs on through the real
// loader: the entry point declares one endpoint on a port of the system's
// choosing.
func loadReader(t *testing.T) config.Reader {
	t.Helper()
	document := fmt.Sprintf(`{"http": {"endpoints": {%q: {"address": "127.0.0.1:0"}}}}`, guardedEndpoint)

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "RATELIMITTEST", DefaultLocator: "file:" + path}},
	})
	// The declaration reaches the load as the resource of a registered module,
	// which is the only way one ever reaches it.
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})

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

// send issues one request over a connection of its own and reads the response
// to its end.
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

// requireProblem asserts the default error shape: this status, the problem
// details media type, and a body holding exactly the members a default response
// writes for a Problem that carries a status and nothing else.
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

// requireRetryAfter asserts the response carries Retry-After as a whole number
// of seconds, and returns it.
func requireRetryAfter(t *testing.T, got response) int {
	t.Helper()
	raw := got.header.Get("Retry-After")
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 1 {
		t.Fatalf("Retry-After is %q, want a whole number of seconds, at least 1", raw)
	}
	return seconds
}

// capturingResponder is an ErrorResponder that keeps the Problems it is asked to
// write, and writes a shape of its own: a media type and a body the default
// shape never produces.
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

// entryRecorder is a slog handler that keeps two things of what the entry point
// writes: the address of its bound socket, which is the only way a client
// outside the process learns the port the system picked, and the listing of the
// layers on its chain.
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

// lockedBuffer is a buffer the signal's lines can be written to from the
// goroutines that serve requests while a case reads it.
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
