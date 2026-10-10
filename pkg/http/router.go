package http

import (
	"fmt"
	"log/slog"
	pathpkg "path"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// phase is the state of the registration gate. It is one gate for the whole
// module rather than one per endpoint: the stages it follows are the module's.
type phase int

const (
	// phaseDeclared is the state the endpoints are constructed in. The
	// surface exists and can be read, and a write is a call that arrived
	// before the gate opened.
	phaseDeclared phase = iota
	// phaseOpen is the window registrations are accepted in. It opens in this
	// module's Migrate callback and closes in its Start callback, and because
	// a stage runs its callbacks one module after another, that interval
	// covers the global Init stage whole and reaches into the rest of the
	// Migrate stage and the Start callbacks ordered ahead of this module.
	phaseOpen
	// phaseSealed is everything after that window. The chains are assembled
	// from what was registered, and a later registration would reach no
	// chain.
	phaseSealed
)

// router is this module's product and the implementation of the Router
// capability. It holds the endpoints configuration declared and the gate their
// registration surfaces share.
//
// The endpoint set is fixed at construction, so a lookup needs no lock; the
// mutex covers the gate and the registrations, which arrive while the gate is
// open, from whatever goroutine a registrant chose to use.
type router struct {
	mu        sync.Mutex
	phase     phase
	endpoints map[string]*endpoint
	// names is the endpoint names in configuration order, kept for the
	// messages that list what does exist and for the order the endpoints are
	// assembled, bound and stopped in.
	names []string

	// injected is the logger put into each request's context, nil when no
	// module delivers the Logger capability.
	injected *slog.Logger
	// responder is what decides the shape of the error responses this module
	// writes, nil when no module delivers the ErrorResponder capability.
	responder ErrorResponder
	// logger is where this module's own diagnostics go. Unlike injected it
	// is never nil: with no Logger capability it is the process default.
	logger *slog.Logger
}

var _ Router = (*router)(nil)

// newRouter builds the endpoints from configuration. Nothing is bound here:
// the address is bound in Serve, and until then an endpoint is a registration
// surface with no listener behind it.
func newRouter(settings []endpointSettings) *router {
	// The registration surface tries each route on an engine of its own, so
	// gin's process-wide mode is settled before the first one exists.
	setReleaseMode()
	r := &router{endpoints: make(map[string]*endpoint, len(settings))}
	for _, s := range settings {
		r.endpoints[s.name] = &endpoint{settings: s, gate: r}
		r.names = append(r.names, s.name)
	}
	return r
}

// Endpoint returns a declared endpoint. It is not behind the gate: a caller may
// look an endpoint up in any stage, and Accepting is readable throughout the
// run.
//
// An undeclared name is an error rather than a panic, because the set of
// endpoints comes from configuration, which is data: a registrant naming an
// endpoint the host did not configure has made no programming error, and the
// failure belongs to the startup its caller aborts. The text lists the names
// that do exist, because picking one of them is the fixing action; there is no
// fallback to a default endpoint.
func (r *router) Endpoint(name string) (Endpoint, error) {
	e, ok := r.endpoints[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q. The endpoints this assembly declares are %s. "+
			"Either register on one of them or add %q to %s.%s in the configuration",
			ErrUnknownEndpoint, name, r.declaredNames(), name, configNamespace, endpointsItem)
	}
	return e, nil
}

// Endpoints returns the declared endpoint names in ascending order, in a slice
// built for the call. The set is fixed at construction, so it is not behind the
// gate and needs no lock: a caller may ask in any stage.
//
// The order is sorted here rather than taken from names, which holds the order
// the settings arrived in: a caller reading the set gets one order whatever
// order the endpoints were declared in.
func (r *router) Endpoints() []string {
	return slices.Sorted(slices.Values(r.names))
}

// declaredNames renders the endpoint names for a message, saying so plainly
// when there are none: "the endpoints are " followed by nothing reads as a
// truncated message rather than as the state it describes.
func (r *router) declaredNames() string {
	if len(r.names) == 0 {
		return "none: the configuration declares no endpoint at all"
	}
	return quotedList(r.Endpoints())
}

// open lets registrations through. It is called from this module's Migrate
// callback, which runs inside the Migrate stage, before the assembly enters
// Init.
//
// Whether it is called at all follows from the depth of the drive: one that
// stops short of Migrate leaves the gate shut and reaches no Init, so there is
// no registration for a shut gate to turn away.
func (r *router) open() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = phaseOpen
}

// seal stops accepting registrations. It is called from this module's Start
// callback, which runs inside the Start stage, after the Init stage has ended.
//
// The pair of borrowed stages is what frames the global Init window, and the
// window it leaves open is wider than that stage. Registration is accepted from
// this module's Migrate callback until this module's Start callback, and a
// stage runs its callbacks one module after another, so the interval also
// covers the rest of the Migrate stage and the Start callbacks ordered ahead of
// this module; what those register is accepted and takes effect. Opening in
// this module's own Init would be too late: Lookup does not filter by Requires,
// so a module that never declared a dependency on Router may be constructed and
// initialised before this one and still legally register in its own Init.
// Sealing in this module's own Serve would close the gate later still,
// accepting the rest of the Start stage and the Serve callbacks of every module
// ordered ahead of this one, none of which reaches a chain.
//
// A drive that stops exactly at Migrate opens the gate and never reaches this,
// so the gate stays open for the rest of the process. Such a run has no Init,
// no registration and no traffic behind it.
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

// route is one method and path bound to a chain of handlers, as the
// registration recorded it. Whether two routes conflict is gin's judgement and
// is made when they are mounted in Serve, not here.
type route struct {
	method   string
	path     string
	handlers []gin.HandlerFunc
}

// String renders a route the way a diagnostic names it: the method and the
// path as they were registered.
func (r route) String() string { return r.method + " " + r.path }

// endpoint is one listening endpoint: the registration surface modules write
// to, and the listener the chain is served from once Serve has bound it.
type endpoint struct {
	settings endpointSettings
	// gate is the module-wide registration gate. The endpoint's own
	// registrations are recorded under its mutex.
	gate *router

	routes []route
	layers []layer

	// socket carries everything that exists only between Serve and the end
	// of the shutdown: the server, the listening socket and the drain.
	socket listener
}

var _ Endpoint = (*endpoint)(nil)

// Route binds handlers to a method and a path on this endpoint.
//
// What can be known from this one registration is checked here, and a failure
// panics, which is the class the surface panics for alongside an illegal
// capability token: no handler or a nil one, which gin accepts and which would
// only crash when a request arrives; a path that is not in the form gin keeps
// it in, which would leave a layer that compares Context.FullPath with the
// registered path forever unmatched; and a method or path gin refuses on its
// own.
//
// A conflict between two routes is not of that class and is not judged here:
// a registration records a declaration, and whether the set of them conflicts
// is a property of the mounted set, reported as a startup failure when the
// chain is built.
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

// ginPath is the form gin keeps a route's path in. Joining the path onto the
// engine's root cleans it, and a trailing slash the registrant wrote is kept
// as written.
func ginPath(path string) string {
	cleaned := pathpkg.Join("/", path)
	if strings.HasSuffix(path, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

// routeRefusal offers the route to an engine of its own and reports what gin
// raised, or nil when it took it. gin refuses a route by panicking, whether
// the method or path is malformed or the route clashes with another one, and
// the two are told apart by what else is on the engine: nothing is on this
// one, so anything raised here is about this route alone.
//
// The engine is thrown away with the call, so it costs one engine per route
// registered.
func routeRefusal(method, path string, handlers []gin.HandlerFunc) (raised any) {
	defer func() { raised = recover() }()
	gin.New().Handle(method, path, handlers...)
	return nil
}

// Use registers a middleware layer on this endpoint. The layer applies to every
// request on the endpoint, including one that matches no route; a layer that
// should apply to some routes only is passed as one of the handlers of those
// routes.
//
// The declaration is checked here rather than when the chain is ordered, so an
// illegal capability token names the call that wrote it while that call is
// still on the stack.
func (e *endpoint) Use(mw Middleware) {
	if mw.Handler == nil {
		panic(fmt.Sprintf("http: endpoint %q: Use registered middleware %q with a nil Handler, "+
			"so the layer would do nothing. Give Handler the gin handler this layer is made "+
			"of", e.settings.name, mw.Name))
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

// registeredLayer turns a registration into the graph node the ordering reads.
//
// The capabilities a layer stands for are what the After and Before
// constraints of the other layers resolve against, and they come from the
// registration's own Provides. They cannot come from anywhere else: the
// surface carries no registrant identity, so the layer is the only thing on
// this path that can say what it stands for.
func registeredLayer(mw Middleware) layer {
	return layer{mw: mw, provides: mw.Provides}
}

// Accepting reports whether this endpoint is still taking requests. It is not
// behind the gate and is safe to call from any goroutine at any moment: before
// the address is bound and after Stop it reports false.
func (e *endpoint) Accepting() bool { return e.socket.isAccepting() }
