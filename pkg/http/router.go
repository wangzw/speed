package http

import (
	"fmt"
	"log/slog"
	nethttp "net/http"
	pathpkg "path"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

type phase int

const (
	// phaseDeclared is the state the endpoints are constructed in.
	phaseDeclared phase = iota
	// phaseOpen is the window registrations are accepted in.
	phaseOpen
	// phaseSealed is everything after that window.
	phaseSealed
)

type router struct {
	mu        sync.Mutex
	phase     phase
	listeners map[string]*listener
	endpoints map[string]*endpoint
	// listenerNames is the listener names in configuration order, kept for the order the
	// listeners are assembled, bound and stopped in.
	listenerNames []string
	// names is the endpoint names in configuration order, kept for the messages that list
	// what does exist and for the order the endpoints are assembled in.
	names []string

	// injected is the logger put into each request's context, nil when no module delivers
	// the Logger capability.
	injected *slog.Logger
	// responder is what decides the shape of the error responses this module
	// writes, nil when no module delivers the ErrorResponder capability.
	responder ErrorResponder
	// localizer renders the text of the refusals this module writes itself, nil when no
	// module delivers the Localizer capability.
	localizer i18n.Localizer
	// resolver names the language of the default problem shape, nil when no module
	// delivers the Resolver capability.
	resolver i18n.Resolver
	// logger is where this module's own diagnostics go. Unlike injected it
	// is never nil: with no Logger capability it is the process default.
	logger *slog.Logger
}

var _ Router = (*router)(nil)

func newRouter(listeners []listenerSettings, endpoints []endpointSettings) *router {
	setReleaseMode()
	r := &router{
		listeners: make(map[string]*listener, len(listeners)),
		endpoints: make(map[string]*endpoint, len(endpoints)),
	}
	for _, s := range listeners {
		r.listeners[s.name] = &listener{settings: s}
		r.listenerNames = append(r.listenerNames, s.name)
	}
	for _, s := range endpoints {
		l := r.listeners[s.listenerName]
		e := &endpoint{settings: s, gate: r, listener: l}
		r.endpoints[s.name] = e
		r.names = append(r.names, s.name)
		l.endpoints = append(l.endpoints, e)
	}
	return r
}

// Endpoint returns a declared endpoint. It is not behind the gate: a caller may look an
// endpoint up in any stage, and Accepting is readable throughout the run.
func (r *router) Endpoint(name string) (Endpoint, error) {
	e, ok := r.endpoints[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q. The endpoints this assembly declares are %s. "+
			"Either register on one of them or add %q to %s.%s in the configuration",
			ErrUnknownEndpoint, name, r.declaredNames(), name, configNamespace, endpointsItem)
	}
	return e, nil
}

// Endpoints returns the declared endpoint names in ascending order, in a slice built
// for the call.
func (r *router) Endpoints() []string {
	return slices.Sorted(slices.Values(r.names))
}

func (r *router) declaredNames() string {
	if len(r.names) == 0 {
		return "none: the configuration declares no endpoint at all"
	}
	return quotedList(r.Endpoints())
}

func (r *router) open() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = phaseOpen
}

func (r *router) seal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = phaseSealed
}

// write performs one registration behind the gate.
//
// A registration outside the open window panics rather than returning an error,
// for the reason the surface returns nothing at all: it is a wiring mistake at
// the call site, and a silently dropped registration shows up as a route nobody
// can reach, long after the call that caused it.
func (r *router) write(endpoint, call string, record func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.phase {
	case phaseOpen:
		record()
	case phaseDeclared:
		panic(fmt.Sprintf("http: endpoint %q: %s was called before the registration gate "+
			"opened. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. Move the call "+
			"into the module's Init callback: every instance exists by then, which is what "+
			"makes it the stage modules wire each other up in", endpoint, call))
	case phaseSealed:
		panic(fmt.Sprintf("http: endpoint %q: %s was called after the registration gate "+
			"closed. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. The chain of "+
			"each endpoint is assembled once in Serve, so this registration would reach no "+
			"chain at all. Move the call into the module's Init callback", endpoint, call))
	}
}

type route struct {
	method   string
	path     string
	handlers []gin.HandlerFunc
}

// String renders a route the way a diagnostic names it: the method and the path as they
// were registered.
func (r route) String() string { return r.method + " " + r.path }

type endpoint struct {
	settings endpointSettings
	// gate is the module-wide registration gate.
	gate *router
	// listener is the socket this endpoint is served from.
	listener *listener

	routes []route
	layers []layer

	// handler is the assembled chain this endpoint serves requests with, set by Serve.
	handler nethttp.Handler
}

var _ Endpoint = (*endpoint)(nil)

// Route binds handlers to a method and a path on this endpoint.
func (e *endpoint) Route(method, path string, handlers ...gin.HandlerFunc) {
	if len(handlers) == 0 {
		panic(fmt.Sprintf("http: endpoint %q: Route(%q, %q) has no handler to bind. "+
			"Pass the handler this route is served by", e.settings.name, method, path))
	}
	for i, h := range handlers {
		if h == nil {
			panic(fmt.Sprintf("http: endpoint %q: Route(%q, %q) was given a nil handler at "+
				"position %d, which gin accepts and which would crash when a request "+
				"reaches it. Pass a handler at every position", e.settings.name, method, path, i))
		}
	}
	if normal := ginPath(path); normal != path {
		panic(fmt.Sprintf("http: endpoint %q: Route(%q, %q) was given a path that is not in "+
			"the form gin keeps it in, which is %q. Context.FullPath reports that form, so a "+
			"layer comparing it with the registered path would never match this route. "+
			"Write the path as %q", e.settings.name, method, path, normal, normal))
	}
	if refused := routeRefusal(method, path, handlers); refused != nil {
		panic(fmt.Sprintf("http: endpoint %q: Route(%q, %q) was given a method or a path gin "+
			"refuses on its own: %v. The grammar is gin's, not this package's; correct it "+
			"at this call", e.settings.name, method, path, refused))
	}
	e.gate.write(e.settings.name, "Route", func() {
		e.routes = append(e.routes, route{method: method, path: path, handlers: slices.Clone(handlers)})
	})
}

func ginPath(path string) string {
	cleaned := pathpkg.Join("/", path)
	if strings.HasSuffix(path, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

func routeRefusal(method, path string, handlers []gin.HandlerFunc) (raised any) {
	defer func() { raised = recover() }()
	gin.New().Handle(method, path, handlers...)
	return nil
}

// Use registers a middleware layer on this endpoint. The layer applies to every request
// on the endpoint, including one that matches no route; a layer that should apply to
// some routes only is passed as one of the handlers of those routes.
func (e *endpoint) Use(mw Middleware) {
	if mw.Handler == nil {
		panic(fmt.Sprintf("http: endpoint %q: Use registered middleware %q with a nil Handler, "+
			"so the layer would do nothing. Give Handler the gin handler this layer is made "+
			"of", e.settings.name, mw.Name))
	}
	if !validStage(mw.Stage) {
		panic(fmt.Sprintf("http: endpoint %q: Use registered middleware %q with Stage %d, which "+
			"is not a declared stage. A layer has to say which category it belongs to, and the "+
			"zero value is not one: write StageObserve, StageGuard, StageIdentify, StageThrottle, "+
			"StageRecord or StageAuthorize on the registration", e.settings.name, mw.Name, mw.Stage))
	}
	for j, token := range mw.Provides {
		capabilityKey(token, declarationSite(e.settings.name, mw.Name, "Provides", j))
	}
	for j, token := range mw.After {
		capabilityKey(token, declarationSite(e.settings.name, mw.Name, "After", j))
	}
	for j, token := range mw.Before {
		capabilityKey(token, declarationSite(e.settings.name, mw.Name, "Before", j))
	}
	e.gate.write(e.settings.name, "Use", func() {
		e.layers = append(e.layers, registeredLayer(mw))
	})
}

func registeredLayer(mw Middleware) layer {
	return layer{mw: mw, provides: mw.Provides}
}

// Accepting reports whether this endpoint is still taking requests.
func (e *endpoint) Accepting() bool { return e.listener.isAccepting() }
