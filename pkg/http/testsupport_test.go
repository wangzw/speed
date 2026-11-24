package http

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/log"
)

func requestBody(mediaType string) *RequestBody {
	return &RequestBody{MediaType: mediaType}
}

func parseProductionFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory failed: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s failed: %v", name, err)
		}
		if file.Name.Name != "http" {
			t.Fatalf("%s declares package %s, not http", name, file.Name.Name)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("no production file was parsed, so every pin built on this would pass vacuously")
	}
	return fset, files
}

// spyWriter is a nethttp.ResponseWriter that records what was actually done to it. A httptest.ResponseRecorder cannot tell "WriteHeader was never called" from "WriteHeader(200) was called", and that is the distinction a test of what the fallback wrote turns on.
type spyWriter struct {
	header      nethttp.Header
	status      int
	wroteHeader bool
	body        []byte
	writeErr    error
}

func newSpyWriter() *spyWriter {
	return &spyWriter{header: nethttp.Header{}}
}

func (s *spyWriter) Header() nethttp.Header { return s.header }

func (s *spyWriter) WriteHeader(status int) {
	s.wroteHeader = true
	s.status = status
}

func (s *spyWriter) Write(p []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	s.body = append(s.body, p...)
	return len(p), nil
}

var _ nethttp.ResponseWriter = (*spyWriter)(nil)

type stubReader struct {
	cfg  moduleConfig
	err  error
	path string
}

func (s *stubReader) Decode(path string, target any) error {
	s.path = path
	if s.err != nil {
		return s.err
	}
	dst, ok := target.(*moduleConfig)
	if !ok {
		return fmt.Errorf("stub reader: this module decodes into *http.moduleConfig, got %T", target)
	}
	*dst = s.cfg
	return nil
}

var _ config.Reader = (*stubReader)(nil)

// recordingHandler keeps every record written through it, so a test can assert
// what this module said rather than that it said something. It is written to
// from the accept loop's own goroutine, hence the mutex.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func newRecordingLogger() (*slog.Logger, *recordingHandler) {
	h := &recordingHandler{}
	return slog.New(h), h
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) at(level slog.Level) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		if r.Level != level {
			continue
		}
		text := r.Message
		r.Attrs(func(a slog.Attr) bool {
			text += fmt.Sprintf(" %s=%v", a.Key, a.Value)
			return true
		})
		out = append(out, text)
	}
	return out
}

func (h *recordingHandler) first(msg string) (map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := make(map[string]string)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		return attrs, true
	}
	return nil, false
}

type gateHandler struct {
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func newGateHandler(t *testing.T) *gateHandler {
	t.Helper()
	g := &gateHandler{
		entered:  make(chan struct{}, 16),
		release:  make(chan struct{}),
		finished: make(chan struct{}, 16),
	}
	// The handler is released whatever the test does, so a failing assertion
	// leaves no goroutine parked on the channel for the rest of the binary.
	t.Cleanup(g.letGo)
	return g
}

func (g *gateHandler) ServeHTTP(w nethttp.ResponseWriter, _ *nethttp.Request) {
	g.entered <- struct{}{}
	<-g.release
	w.WriteHeader(nethttp.StatusNoContent)
	g.finished <- struct{}{}
}

func (g *gateHandler) gin(c *gin.Context) { g.ServeHTTP(c.Writer, c.Request) }

func (g *gateHandler) letGo() {
	select {
	case <-g.release:
	default:
		close(g.release)
	}
}

// waitFor takes one value from ch, failing the test if none arrives. Every wait
// in these tests carries its own deadline: relying on go test's global timeout
// turns a specific assertion into a whole-binary panic ten minutes later.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	limit := waitDeadline()
	select {
	case <-ch:
	case <-time.After(limit):
		t.Fatalf("timed out after %s waiting for %s", limit, what)
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	limit := waitDeadline()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", limit, what)
}

func waitDeadline() time.Duration {
	return coretest.Scale(30 * time.Second)
}

func testSettings(name string) endpointSettings {
	return endpointSettings{
		name:         name,
		listenerName: "backend",
		prefixes:     []string{"/"},
		maxBodyBytes: defaultMaxBodyBytes,
	}
}

// testListener is one listener's settings with the timeouts a test wants:
// short enough that a hung assertion fails rather than hangs, long enough that
// a loaded machine does not fail a passing one.
func testListener(name string) listenerSettings {
	return listenerSettings{
		name:              name,
		address:           "127.0.0.1:0",
		readHeaderTimeout: 5 * time.Second,
		drainTimeout:      5 * time.Second,
	}
}

func supplyConfig(reg *core.Registry, cfg moduleConfig) {
	reg.Register(config.Module())
	reg.Supply(&stubReader{cfg: cfg}, config.ModuleName)
}

type stubLogger struct{ logger *slog.Logger }

func (s stubLogger) Named(string) *slog.Logger { return s.logger }
func (s stubLogger) Redaction() log.Redaction  { return noRedaction{} }

type namingLogger struct {
	stubLogger
	mu    sync.Mutex
	names []string
}

func (n *namingLogger) Named(name string) *slog.Logger {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.names = append(n.names, name)
	return n.logger
}

func (n *namingLogger) asked() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.names...)
}

var _ log.Logger = (*namingLogger)(nil)

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

var _ log.Logger = stubLogger{}

// capA and capB are capabilities a probe module really delivers, product and declaration both. A layer naming one in Provides then stands for something the assembly contains, which is the state the constraints have to be exercised in: a layer pointing at a capability nobody delivers drops its constraint.
type capA interface{ standsForA() }

type capB interface{ standsForB() }

type deliveredA struct{}

type deliveredB struct{}

func (deliveredA) standsForA() {}

func (deliveredB) standsForB() {}

var (
	_ capA = deliveredA{}
	_ capB = deliveredB{}
)

type stageProbe struct {
	name     string
	requires []core.Requirement
	provides []core.Provision
	// product is what New hands back. It has to satisfy every capability in
	// provides, because core checks the product against the declaration as
	// it constructs.
	product any

	onNew     func(*core.Registry) error
	onMigrate func(*core.Registry) error
	onInit    func(*core.Registry) error
	onStart   func(*core.Registry) error
	onServe   func(*core.Registry) error
	onStop    func(*core.Registry) error
	onClose   func(*core.Registry) error
}

func (p *stageProbe) module() core.Module {
	stage := func(f func(*core.Registry) error) func(context.Context, *core.Registry, any) error {
		if f == nil {
			return nil
		}
		return func(_ context.Context, reg *core.Registry, _ any) error { return f(reg) }
	}
	return core.Module{
		Name:     p.name,
		Requires: p.requires,
		Provides: p.provides,
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			if p.onNew != nil {
				if err := p.onNew(reg); err != nil {
					return nil, err
				}
			}
			return p.product, nil
		},
		Migrate: stage(p.onMigrate),
		Init:    stage(p.onInit),
		Start:   stage(p.onStart),
		Serve:   stage(p.onServe),
		Stop:    stage(p.onStop),
		Close:   stage(p.onClose),
	}
}

func requiresRouter() []core.Requirement {
	return []core.Requirement{{Token: (*Router)(nil)}}
}

func inOrder(steps ...func(*core.Registry) error) func(*core.Registry) error {
	return func(reg *core.Registry) error {
		for _, step := range steps {
			if err := step(reg); err != nil {
				return err
			}
		}
		return nil
	}
}

func routeOn(endpoint, method, path string, handlers ...gin.HandlerFunc) func(*core.Registry) error {
	return func(reg *core.Registry) error {
		e, err := endpointFrom(reg, endpoint)
		if err != nil {
			return err
		}
		e.Route(method, path, handlers...)
		return nil
	}
}

func useLayer(endpoint string, mw Middleware) func(*core.Registry) error {
	return func(reg *core.Registry) error {
		e, err := endpointFrom(reg, endpoint)
		if err != nil {
			return err
		}
		e.Use(mw)
		return nil
	}
}

// assembly is one registry going through the whole lifecycle on its own
// goroutine, with the two handles a test needs: the cancellation that ends the
// run, and the error Run came back with.
type assembly struct {
	reg    *core.Registry
	cancel context.CancelFunc
	result chan error

	mu       sync.Mutex
	finished bool
	err      error
}

func startAssembly(t *testing.T, cfg moduleConfig, modules ...core.Module) *assembly {
	t.Helper()
	reg := core.New()
	supplyConfig(reg, cfg)
	for _, m := range modules {
		reg.Register(m)
	}
	return runAssembly(t, reg)
}

func runAssembly(t *testing.T, reg *core.Registry) *assembly {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a := &assembly{reg: reg, cancel: cancel, result: make(chan error, 1)}
	go func() { a.result <- reg.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		a.wait(t)
	})
	return a
}

func (a *assembly) wait(t *testing.T) error {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return a.err
	}
	limit := waitDeadline()
	select {
	case err := <-a.result:
		a.finished, a.err = true, err
		return err
	case <-time.After(limit):
		t.Fatalf("timed out after %s waiting for Run to return", limit)
		return nil
	}
}

// waitForOrEnd takes one value from ch, failing the test with what Run came back with if Run ends first: a startup that failed would otherwise show only as a wait that ran out its deadline. The result is kept as wait keeps it.
func (a *assembly) waitForOrEnd(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	limit := waitDeadline()
	select {
	case <-ch:
	case err := <-a.result:
		a.mu.Lock()
		a.finished, a.err = true, err
		a.mu.Unlock()
		t.Fatalf("Run came back with %v before %s", err, what)
	case <-time.After(limit):
		t.Fatalf("timed out after %s waiting for %s", limit, what)
	}
}

func (a *assembly) stop(t *testing.T) error {
	t.Helper()
	a.cancel()
	return a.wait(t)
}

// routerFrom takes this module's product out of a registry. It reports a
// failure rather than failing a test, because the callers that need it most
// are the probe callbacks, which run on the goroutine driving Run.
func routerFrom(reg *core.Registry) (*router, error) {
	delivered, err := core.Resolve[Router](reg)
	if err != nil {
		return nil, fmt.Errorf("taking up the Router capability: %w", err)
	}
	r, ok := delivered.(*router)
	if !ok {
		return nil, fmt.Errorf("the Router capability is delivered by %T, not by this module", delivered)
	}
	return r, nil
}

func endpointFrom(reg *core.Registry, name string) (Endpoint, error) {
	r, err := routerFrom(reg)
	if err != nil {
		return nil, err
	}
	e, err := r.Endpoint(name)
	if err != nil {
		return nil, fmt.Errorf("looking up endpoint %q: %w", name, err)
	}
	return e, nil
}

func boundAddr(r *router, name string) (string, error) {
	e, ok := r.endpoints[name]
	if !ok {
		return "", fmt.Errorf("endpoint %q is not declared in this assembly", name)
	}
	if e.listener == nil {
		return "", fmt.Errorf("endpoint %q hangs on no listener in this assembly", name)
	}
	addr := e.listener.addr()
	if addr == nil {
		return "", fmt.Errorf("endpoint %q never bound an address", name)
	}
	return addr.String(), nil
}

func getStatus(ctx context.Context, addr, path string) (int, error) {
	resp, err := getResponse(ctx, addr, path)
	if err != nil {
		return 0, err
	}
	return resp.status, nil
}

type answer struct {
	status int
	header nethttp.Header
	body   string
	// bodyErr is what reading the body ended with. A response the server cut
	// short surfaces here rather than as an error from the request.
	bodyErr error
}

// getResponse issues one GET against a bound endpoint over a connection of its own, so what a server does to its connection is seen by this request alone. The client asks for no compression and decodes none, so the headers and body are what the server sent; an error means no response at all.
func getResponse(ctx context.Context, addr, path string) (answer, error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return answer{}, err
	}
	client := &nethttp.Client{
		Timeout:   waitDeadline(),
		Transport: &nethttp.Transport{DisableKeepAlives: true, DisableCompression: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer resp.Body.Close()
	body, bodyErr := io.ReadAll(resp.Body)
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body), bodyErr: bodyErr}, nil
}

func getTracingInterim(t *testing.T, addr, path string) ([]int, answer) {
	t.Helper()
	var interim []int
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
		interim = append(interim, code)
		return nil
	}}
	request, err := nethttp.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace),
		nethttp.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &nethttp.Client{
		Timeout:   waitDeadline(),
		Transport: &nethttp.Transport{DisableKeepAlives: true, DisableCompression: true},
	}
	resp, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	body, bodyErr := io.ReadAll(resp.Body)
	return interim, answer{status: resp.StatusCode, header: resp.Header, body: string(body), bodyErr: bodyErr}
}

func serveOnce(h nethttp.Handler, method, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func filler(c *gin.Context) { c.Status(nethttp.StatusNoContent) }

func endpointsAt(drain time.Duration, names ...string) moduleConfig {
	cfg := moduleConfig{
		Listeners: make(map[string]listenerConfig, len(names)),
		Endpoints: make(map[string]endpointConfig, len(names)),
	}
	for _, name := range names {
		cfg.Listeners[name] = listenerConfig{Address: "127.0.0.1:0", DrainTimeout: &drain}
		cfg.Endpoints[name] = endpointConfig{Listener: name}
	}
	return cfg
}

func serveRoutes(t *testing.T, responder ErrorResponder, register func(Endpoint)) (string, *recordingHandler) {
	t.Helper()
	logger, records := newRecordingLogger()
	modules := []core.Module{
		Module(),
		(&stageProbe{
			name:     "probe.delivers-the-logger",
			provides: []core.Provision{{Token: (*log.Logger)(nil)}},
			product:  stubLogger{logger: logger},
		}).module(),
	}
	if responder != nil {
		modules = append(modules, (&stageProbe{
			name:     "probe.delivers-the-responder",
			provides: []core.Provision{{Token: (*ErrorResponder)(nil)}},
			product:  responder,
		}).module())
	}
	serving := make(chan struct{})
	modules = append(modules, (&stageProbe{
		name:     "probe.registers-the-routes",
		requires: requiresRouter(),
		onInit: func(reg *core.Registry) error {
			e, err := endpointFrom(reg, "public")
			if err != nil {
				return err
			}
			register(e)
			return nil
		},
		onServe: func(*core.Registry) error {
			close(serving)
			return nil
		},
	}).module())

	a := startAssembly(t, oneEndpoint("public"), modules...)
	a.waitForOrEnd(t, serving, "the endpoint to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "public")
	if err != nil {
		t.Fatalf("finding the address the endpoint bound: %v", err)
	}
	return addr, records
}
