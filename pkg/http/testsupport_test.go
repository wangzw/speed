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

// requestBody is one declared request body with the given media type.
func requestBody(mediaType string) *RequestBody {
	return &RequestBody{MediaType: mediaType}
}

// parseProductionFiles parses this package's production files. The test files
// are excluded: what several tests here pin is the package's own shape, and a
// declaration made by a test is not part of it.
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

// spyWriter is a nethttp.ResponseWriter that records what was actually done to
// it. A httptest.ResponseRecorder cannot tell "WriteHeader was never called"
// from "WriteHeader(200) was called", and that is the distinction a test of what
// the fallback wrote turns on.
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

// stubReader is a config.Reader handing back a moduleConfig the test wrote.
// The real reader is the one config loads from documents and arguments, and
// readThrough takes a test down that path; the tests that drive the callbacks
// state the configuration as a value instead, so none of them writes a document
// or depends on the arguments a test binary is started with.
type stubReader struct {
	cfg moduleConfig
	err error
	// path records what the caller asked for, so a test can pin that this
	// module decodes its own section rather than somebody else's.
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

// at returns the records written at one level, rendered as message plus
// attributes, which is what an assertion on the text reads.
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

// first returns the attributes of the first record written with msg, each
// value rendered as text.
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

// gateHandler is a handler a test holds inside: it reports that a request
// arrived and stays there until the test lets go. The drain is only observable
// with a request in flight across the shutdown.
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

// gin is the same handler as a route's: it holds the request on the gin
// context's writer, which is the one a registered handler is given.
func (g *gateHandler) gin(c *gin.Context) { g.ServeHTTP(c.Writer, c.Request) }

// letGo releases every request being held, once, however often it is called.
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
	select {
	case <-ch:
	case <-time.After(waitDeadline):
		t.Fatalf("timed out after %s waiting for %s", waitDeadline, what)
	}
}

// waitUntil polls a condition to the same deadline, for the states that are
// reached without a channel to wait on.
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", waitDeadline, what)
}

// waitDeadline bounds every wait in this package's tests.
var waitDeadline = coretest.Scale(30 * time.Second)

// testSettings is one endpoint's settings: the claims that select it, the body
// limit and the proxy settings that belong to an endpoint's chain rather than
// to its listener. An endpoint on a listener of its own claims "/", which is
// what omitting the key in configuration gives.
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

// The fixtures below drive a whole assembly: core.New() with a hand-built set
// of modules, and Run advancing every stage the way a host's would. They live
// here rather than in the file of whichever test needed one first, so that the
// stubs are one shape shared instead of several that drift.
//
// A probe's position in the assembly comes from what it declares and from
// nothing else. core promises dependency order; how it linearises what
// dependency order leaves open is its own implementation choice, so no fixture
// here and no test built on one may read an order out of module names.

// supplyConfig gives the registry the reader the assembly reads its endpoints
// through, the way a host does: the reader is the instance of the config module,
// supplied to it before the drive, so it exists from this call on and answers
// every lookup for the capability, Prepare's included.
//
// A registry made with core.New does not hold the config module the process
// registry has from the config package's init, and an instance can only be
// supplied to a module that is registered, so the module is registered here.
// The call names it, so the reader belongs to it whatever else a test registers.
// What the reader hands back is the configuration the test wrote.
func supplyConfig(reg *core.Registry, cfg moduleConfig) {
	reg.Register(config.Module())
	reg.Supply(&stubReader{cfg: cfg}, config.ModuleName)
}

// stubLogger delivers the Logger capability over a logger the test holds. A
// module carrying it is constructed before this one and stopped after it: the
// dependency on Logger is optional, and an optional requirement whose provider
// is present still draws the ordering edge.
type stubLogger struct{ logger *slog.Logger }

func (s stubLogger) Named(string) *slog.Logger { return s.logger }
func (s stubLogger) Redaction() log.Redaction  { return noRedaction{} }

// namingLogger is stubLogger that keeps the name each Named call asked for, so a
// test can read what a module called itself when it took the logger up.
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

// asked is the names Named was called with, in order.
func (n *namingLogger) asked() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.names...)
}

var _ log.Logger = (*namingLogger)(nil)

// noRedaction takes registrations and masks nothing. What an assembly test
// observes of the Logger capability is the ordering it causes, not the masking.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

var _ log.Logger = stubLogger{}

// capA and capB are capabilities a probe module really delivers, product and
// declaration both. A middleware layer that names one of them in Provides is
// then standing for something the assembly actually contains, which is the
// state the constraints have to be exercised in: a layer pointing at a
// capability nobody delivers is the other case, and it drops its constraint.
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

// stageProbe is a module a test puts in an assembly to watch a stage from the
// inside, and to make the registrations a real registrant would make.
//
// A nil callback is left off the descriptor. A callback reports a failed
// observation by returning an error, which fails that stage: the goroutine
// running Run is not the test's, so t.Fatal cannot be called from one, and an
// error carries the text out to where the test reads it.
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

// module renders the probe as the descriptor a registry takes.
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

// requiresRouter is the declaration that puts a probe after this module: it is
// constructed, initialised, started and served once this module has been, and
// stopped and closed before it. It is the only thing a test may rely on for
// that position.
func requiresRouter() []core.Requirement {
	return []core.Requirement{{Token: (*Router)(nil)}}
}

// inOrder runs several stage callbacks one after another, for a probe with
// more than one thing to do in a stage.
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

// routeOn is the stage callback that registers one route through the public
// surface, the path a real registrant takes.
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

// useLayer is the stage callback that registers one middleware layer through
// the public surface.
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

// startAssembly registers the config module with the reader over cfg supplied to
// it, registers the modules, starts Run and hands back the handle. The reader
// goes in first, which is the order a host works in: it is there before anything
// else is registered and long before the drive begins.
//
// A test that has to supply an instance to one of the modules puts the registry
// together itself and hands it to runAssembly: the supply comes after that
// module is registered and before the drive.
func startAssembly(t *testing.T, cfg moduleConfig, modules ...core.Module) *assembly {
	t.Helper()
	reg := core.New()
	supplyConfig(reg, cfg)
	for _, m := range modules {
		reg.Register(m)
	}
	return runAssembly(t, reg)
}

// runAssembly starts Run on a registry the caller has put together and hands
// back the handle.
//
// The run is ended and waited for whatever the test does, so a failing
// assertion leaves no registry serving for the rest of the binary.
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

// wait takes the result of Run, to this package's deadline. The result is
// kept, so the cleanup may ask for it again after the test already has.
func (a *assembly) wait(t *testing.T) error {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return a.err
	}
	select {
	case err := <-a.result:
		a.finished, a.err = true, err
		return err
	case <-time.After(waitDeadline):
		t.Fatalf("timed out after %s waiting for Run to return", waitDeadline)
		return nil
	}
}

// waitForOrEnd takes one value from ch, failing the test with what Run came back
// with if Run ends first: a startup that failed would otherwise show only as a
// wait that ran out its deadline, with nothing saying why. The result is kept as
// wait keeps it.
func (a *assembly) waitForOrEnd(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case err := <-a.result:
		a.mu.Lock()
		a.finished, a.err = true, err
		a.mu.Unlock()
		t.Fatalf("Run came back with %v before %s", err, what)
	case <-time.After(waitDeadline):
		t.Fatalf("timed out after %s waiting for %s", waitDeadline, what)
	}
}

// stop ends the run and reports what Run came back with.
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

// endpointFrom looks one endpoint up through the public surface.
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

// boundAddr is the address an endpoint's listener really bound, which is where a
// request in these tests goes: the configuration asks for port 0 and the
// operating system picks.
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

// getStatus issues one GET against a bound endpoint and reports the status it
// answered with. Unlike get, which only needs a request to be in flight, this
// one is for the assertions that turn on what the chain actually answered.
func getStatus(ctx context.Context, addr, path string) (int, error) {
	resp, err := getResponse(ctx, addr, path)
	if err != nil {
		return 0, err
	}
	return resp.status, nil
}

// answer is what a client got back over a socket: the status line, the headers
// and the body read to its end.
type answer struct {
	status int
	header nethttp.Header
	body   string
	// bodyErr is what reading the body ended with. A response the server cut
	// short surfaces here rather than as an error from the request.
	bodyErr error
}

// getResponse issues one GET against a bound endpoint over a connection of its
// own, so that what a server does to its connection is seen by this request and
// by no other. The client asks for no compression and decodes none, so the
// headers and the body are what the server sent. An error is a request that got
// no response at all.
func getResponse(ctx context.Context, addr, path string) (answer, error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return answer{}, err
	}
	client := &nethttp.Client{
		Timeout:   waitDeadline,
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

// getTracingInterim is getResponse that also reports the informational statuses
// the client saw before the final response: a 103 Early Hints is the one a
// handler sends.
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
		Timeout:   waitDeadline,
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

// serveOnce sends one request through an assembled handler and returns what it
// wrote, for the tests whose subject is the chain and not the socket.
func serveOnce(h nethttp.Handler, method, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

// filler is a route's handler for the tests that need a route to exist and do
// not care what it answers.
func filler(c *gin.Context) { c.Status(nethttp.StatusNoContent) }

// endpointsAt is a configuration with one endpoint per name, each on a listener
// of its own named the same, on a loopback port the operating system picks,
// sharing one drain timeout.
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

// serveRoutes runs a real assembly with one endpoint, "public", and the routes
// the test registers on it, and returns the address it bound together with the
// records the assembly wrote. What it exercises is the socket: the connection
// handling of net/http, which no recorder reproduces.
//
// A module delivering the Logger capability over a recording logger takes part,
// so that the records this module writes for a request — and the ones net/http
// writes for itself, which this module hands to the same logger — are readable.
// responder, when non-nil, is delivered as the ErrorResponder capability.
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
