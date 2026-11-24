package stream

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

type stubBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newStubBus() *stubBus {
	return &stubBus{handlers: make(map[string][]event.Handler)}
}

func (b *stubBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *stubBus) Publish(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range handlers {
		_ = h(ctx, evt)
	}
	return nil
}

func (b *stubBus) subscribed(eventType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[eventType])
}

var _ event.Bus = (*stubBus)(nil)

func inboxEvent(tenant, recipient string) event.Event {
	return event.Event{
		Type: eventInboxCreated,
		Payload: map[string]any{
			"message_id":        "message-1",
			"tenant_id":         tenant,
			"recipient_user_id": recipient,
			"type_key":          "billing.invoice_paid",
		},
	}
}

type mountedRoute struct{ method, path string }

type stubEndpoint struct {
	mu     sync.Mutex
	routes []mountedRoute
}

func (e *stubEndpoint) Route(method, path string, _ ...gin.HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routes = append(e.routes, mountedRoute{method, path})
}

func (e *stubEndpoint) Use(speedhttp.Middleware) {}

func (e *stubEndpoint) Accepting() bool { return true }

func (e *stubEndpoint) registered() []mountedRoute {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]mountedRoute(nil), e.routes...)
}

type stubRouter struct {
	mu        sync.Mutex
	endpoints map[string]*stubEndpoint
	order     []string
}

func newStubRouter(names ...string) *stubRouter {
	r := &stubRouter{endpoints: make(map[string]*stubEndpoint, len(names))}
	for _, name := range names {
		r.endpoints[name] = &stubEndpoint{}
		r.order = append(r.order, name)
	}
	return r
}

func (r *stubRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, ok := r.endpoints[name]
	if !ok {
		return nil, errUnknownEndpoint(name)
	}
	return endpoint, nil
}

func (r *stubRouter) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *stubRouter) endpoint(name string) *stubEndpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.endpoints[name]
}

func errUnknownEndpoint(name string) error {
	return &unknownEndpoint{name: name}
}

type unknownEndpoint struct{ name string }

func (e *unknownEndpoint) Error() string { return "the entry point declares no endpoint " + e.name }

type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func standIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type rig struct {
	t      *testing.T
	p      *product
	bus    *stubBus
	engine *gin.Engine
}

func newRig(t *testing.T) *rig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	bus := newStubBus()
	p := &product{
		hub:    &Hub{},
		bus:    bus,
		logger: log.Default(),
		cfg:    resolved{endpoints: []string{"public"}},
	}
	p.subscribe()
	engine := gin.New()
	engine.GET(pathStream, p.guardStream(subscribeOperation.Handle(p.handleStream)))
	return &rig{t: t, p: p, bus: bus, engine: engine}
}

func newTestProduct() (*product, *stubBus) {
	bus := newStubBus()
	p := &product{
		hub:    &Hub{},
		bus:    bus,
		logger: log.Default(),
		cfg:    resolved{endpoints: []string{"public"}},
	}
	return p, bus
}

type plainWriter struct{ w http.ResponseWriter }

func (p plainWriter) Header() http.Header         { return p.w.Header() }
func (p plainWriter) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p plainWriter) WriteHeader(code int)        { p.w.WriteHeader(code) }

type wrapper struct{ w http.ResponseWriter }

func (u wrapper) Header() http.Header         { return u.w.Header() }
func (u wrapper) Write(b []byte) (int, error) { return u.w.Write(b) }
func (u wrapper) WriteHeader(code int)        { u.w.WriteHeader(code) }
func (u wrapper) Unwrap() http.ResponseWriter { return u.w }

// firstConnection waits until the served stream has subscribed a connection
// and returns it, so a case can put a frame on the connection the route holds.
func (r *rig) firstConnection(t *testing.T) *connection {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.p.hub.mu.Lock()
		var found *connection
		for _, list := range r.p.hub.conns {
			if len(list) > 0 {
				found = list[0]
				break
			}
		}
		r.p.hub.mu.Unlock()
		if found != nil {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatal("the served stream subscribed no connection")
		}
		time.Sleep(time.Millisecond)
	}
}

type flushRecorder struct {
	header  http.Header
	mu      sync.Mutex
	buf     bytes.Buffer
	status  int
	flushes chan string
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: make(http.Header), flushes: make(chan string, 8)}
}

func (w *flushRecorder) Header() http.Header { return w.header }

func (w *flushRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *flushRecorder) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = code
}

func (w *flushRecorder) Flush() {
	w.mu.Lock()
	flushed := w.buf.String()
	w.buf.Reset()
	w.mu.Unlock()
	if flushed != "" {
		w.flushes <- flushed
	}
}

// timingRecorder is an http.ResponseWriter that records the order in which the
// answer writes its status, flushes, and body bytes. A case reads it to see
// whether the status and headers went out before the first event was written.
type timingRecorder struct {
	mu           sync.Mutex
	header       http.Header
	body         bytes.Buffer
	status       int
	writes       int
	flushes      int
	emptyFlushes int
	firstContent string
}

func newTimingRecorder() *timingRecorder {
	return &timingRecorder{header: make(http.Header)}
}

func (w *timingRecorder) Header() http.Header { return w.header }

func (w *timingRecorder) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = code
}

func (w *timingRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	return w.body.Write(p)
}

// Flush records the flush and, on the first one, the content type the headers
// carried at that moment. A flush made before any body byte was written is the
// one that establishes the connection.
func (w *timingRecorder) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushes == 0 {
		w.firstContent = w.header.Get("Content-Type")
	}
	w.flushes++
	if w.writes == 0 {
		w.emptyFlushes++
	}
}

type timingSnapshot struct {
	status       int
	writes       int
	flushes      int
	emptyFlushes int
	firstContent string
	body         string
}

func (w *timingRecorder) snapshot() timingSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return timingSnapshot{
		status:       w.status,
		writes:       w.writes,
		flushes:      w.flushes,
		emptyFlushes: w.emptyFlushes,
		firstContent: w.firstContent,
		body:         w.body.String(),
	}
}
