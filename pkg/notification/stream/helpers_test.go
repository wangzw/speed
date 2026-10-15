package stream

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// stubBus is a stand-in event.Bus. It records every subscription by type and,
// on Publish, runs every handler of that type on the caller's own goroutine, so
// a case observes delivery by the time Publish returns.
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

// subscribed reports how many handlers are registered for one event type.
func (b *stubBus) subscribed(eventType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[eventType])
}

var _ event.Bus = (*stubBus)(nil)

// inboxEvent builds an event.Event carrying the payload shape the inbox
// publishes.
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

// mountedRoute is one (method, path) a registration recorded.
type mountedRoute struct{ method, path string }

// stubEndpoint records the routes registered on it.
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

// stubRouter is a stand-in speedhttp.Router over a fixed set of endpoints. A
// name it does not hold is an error, the way the real entry point answers an
// undeclared endpoint.
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

// errUnknownEndpoint is the error a stub router answers an undeclared name
// with.
func errUnknownEndpoint(name string) error {
	return &unknownEndpoint{name: name}
}

type unknownEndpoint struct{ name string }

func (e *unknownEndpoint) Error() string { return "the entry point declares no endpoint " + e.name }

// loggerStub delivers the logging capability over a recorder, so the module's
// startup diagnostic lands in a case's buffer rather than on the test's output.
type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

// noRules takes redaction registrations and masks nothing.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// logBuffer collects what the module logs.
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

// standIn is a module that delivers instance for the tokens named, the way
// another module delivers its capability exclusively.
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

// rig is the module's route mounted on a real gin engine with a stand-in bus,
// so a case reaches the handler over a real HTTP surface.
type rig struct {
	t      *testing.T
	p      *product
	bus    *stubBus
	engine *gin.Engine
}

// newRig builds the product over a stub bus, subscribes it as Init does, and
// mounts the stream route on a fresh gin engine.
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
	engine.GET(pathStream, p.handleStream)
	return &rig{t: t, p: p, bus: bus, engine: engine}
}

// newTestProduct builds a product over a stub bus and this module's logger, for
// a case that drives the Hub and the subscription directly. It hands back the
// stub too, so the case can read what was subscribed.
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

// plainWriter is an http.ResponseWriter with neither Flush nor Unwrap: the
// chain that ends with no flusher.
type plainWriter struct{ w http.ResponseWriter }

func (p plainWriter) Header() http.Header         { return p.w.Header() }
func (p plainWriter) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p plainWriter) WriteHeader(code int)        { p.w.WriteHeader(code) }

// wrapper is an http.ResponseWriter that leaves Unwrap behind and nothing else,
// the way a layer that replaced the writer does.
type wrapper struct{ w http.ResponseWriter }

func (u wrapper) Header() http.Header         { return u.w.Header() }
func (u wrapper) Write(b []byte) (int, error) { return u.w.Write(b) }
func (u wrapper) WriteHeader(code int)        { u.w.WriteHeader(code) }
func (u wrapper) Unwrap() http.ResponseWriter { return u.w }
