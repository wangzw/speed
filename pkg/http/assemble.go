package http

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

// assemble builds this endpoint's final handler, once, from the inside out: a
// gin engine is made and the registered layers are handed to its Use in the
// order the constraints and the preferences give, the routes are mounted on it,
// the fallback closes over the whole of that, and the base sits outside all of
// it.
//
// injected is the logger to put in each request's context, or nil when no
// module delivers the Logger capability. logger is where this assembly's own
// diagnostics go. responder is what decides the shape of the error responses,
// or nil when no module delivers the ErrorResponder capability.
func (e *endpoint) assemble(injected, logger *slog.Logger, responder ErrorResponder) (nethttp.Handler, error) {
	ordered, unlanded, err := orderLayers(e.settings.name, e.layers)
	if err != nil {
		return nil, err
	}

	engine := newEngine()
	// ordered runs from outermost to innermost, and gin runs the handlers
	// given to Use in the order given. Use goes first: gin combines the
	// engine's handlers into a route's chain when the route is added, so a
	// layer registered after the routes would not reach them.
	if raised := useLayers(engine, ordered); raised != nil {
		return nil, fmt.Errorf("%w: endpoint %q: gin refused the %d registered layers: %v. A chain "+
			"holds a limited number of handlers, and the layers, the engine's own and a route's "+
			"count together; register fewer layers on this endpoint or move some of them to "+
			"another endpoint", ErrRouteConflict, e.settings.name, len(ordered), raised)
	}
	if err := e.mount(engine); err != nil {
		return nil, err
	}

	reportUnlanded(logger, unlanded)
	logger.Info("an HTTP endpoint's chain is assembled",
		"endpoint", e.settings.name,
		"routes", len(e.routes),
		"chain", chainDescription(e.settings.name, ordered))
	return e.base(fallback(engine, e.settings.name, responder), injected, responder), nil
}

// useLayers hands the layers to the engine and returns what gin raised, or nil.
// gin refuses a chain longer than it can hold by panicking, and that would
// otherwise leave Serve as a panic instead of a startup failure.
func useLayers(engine *gin.Engine, ordered []layer) (raised any) {
	defer func() { raised = recover() }()
	for _, l := range ordered {
		engine.Use(l.mw.Handler)
	}
	return nil
}

// ginMode is the guard that sets gin's process-wide mode once.
var ginMode sync.Once

// setReleaseMode puts gin in release mode. The mode is process-wide and gin
// prints a line to standard output for every route registered while it is in
// debug mode, which would put this module's records outside the logging
// module's destinations. It is set before the first engine is made, whether
// that engine serves requests or only tries a route.
func setReleaseMode() {
	ginMode.Do(func() { gin.SetMode(gin.ReleaseMode) })
}

// newEngine builds the gin engine of one endpoint, without gin's default
// middleware. One engine per endpoint: a shared one would carry every
// endpoint's routes, and the separation of a management endpoint from a public
// one is the whole point of having several.
//
// The settings are the ones every layer's position depends on. A redirect to
// the path with or without its trailing slash is answered before the engine's
// middleware runs, so a request that gets one would go past every layer;
// redirects are off. A request that matches no route, and one whose method the
// path does not allow, run through all the layers like any other and are
// answered through Fail. The engine trusts no proxy, because gin's default
// takes X-Forwarded-For from any peer, which lets a client choose the address
// Context.ClientIP reports. Context falls back to the request's context, so a
// logger put there is reachable from the gin context as well.
//
// gin writes the Allow header of a 405 into the response before it runs any
// layer, so a layer that refuses such a request — authentication with a 401,
// tenant resolution with a 404, rate limiting with a 429 — would send the
// caller the fact that the path exists and the methods it allows. The first
// handler on the engine takes the header off and holds it; the handler for a
// method the path does not allow puts it back once every layer has let the
// request through.
func newEngine() *gin.Engine {
	setReleaseMode()
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.HandleMethodNotAllowed = true
	engine.ContextWithFallback = true
	// SetTrustedProxies fails only for an address it cannot parse, and nil
	// has none.
	_ = engine.SetTrustedProxies(nil)
	engine.Use(holdAllow)
	engine.NoRoute(func(c *gin.Context) { Fail(c, Problem{Status: nethttp.StatusNotFound}) })
	engine.NoMethod(func(c *gin.Context) {
		if allow, held := c.Get(heldAllowKey); held {
			if methods, isString := allow.(string); isString {
				c.Header("Allow", methods)
			}
		}
		Fail(c, Problem{Status: nethttp.StatusMethodNotAllowed})
	})
	return engine
}

// heldAllowKey is the key holdAllow keeps the Allow header of a 405 under in
// the gin context.
const heldAllowKey = "http:held-allow"

// holdAllow is the first handler on every engine. A request that gin answers
// with 405 arrives with the Allow header already on the response; this takes
// it off and keeps it for the method-not-allowed handler, so that a layer
// refusing the request first does not carry it out. It does nothing to a
// request that has no such header, which is every request that matched a route
// or none.
func holdAllow(c *gin.Context) {
	if allow := c.Writer.Header().Get("Allow"); allow != "" {
		c.Writer.Header().Del("Allow")
		c.Set(heldAllowKey, allow)
	}
	c.Next()
}

// unlandedMsg is the message of the record reporting a constraint that placed
// nothing.
const unlandedMsg = "an HTTP middleware constraint landed on no layer"

// reportUnlanded writes out the constraints that placed nothing, one record
// each, at the level their cause calls for.
//
// This is the whole of their observability. The assembly carries on — a
// capability absent from the process is a legal configuration — so the layer
// that declared itself inside authentication runs anyway, in the position its
// Order happens to give it. The cause is written with each line because the
// two causes are answered by different edits: a capability nobody stands for
// sends the reader looking for the module that should have been on the
// endpoint, while a capability only the declaring layer stands for is that
// layer correcting what it said about itself.
//
// It goes to this module's own logger because core does not export a startup
// diagnostics surface. The level is the cause's; see unlandedCause.level.
func reportUnlanded(logger *slog.Logger, unlanded []unlandedConstraint) {
	for _, u := range unlanded {
		logger.Log(context.Background(), u.cause.level(), unlandedMsg,
			"endpoint", u.endpoint,
			"middleware", u.layer,
			"declaration", fmt.Sprintf("%s[%d]", u.field, u.at),
			"capability", u.capability,
			"cause", string(u.cause))
	}
}

// level is the level a constraint that placed nothing is written at. What
// decides it is whether the assembly can be right as it stands.
//
// A capability no layer on the endpoint stands for is what a legal assembly
// produces whenever the module standing for it is absent from the process or
// does not guard that endpoint, so it is written at Info, beside the chain
// listing it explains. A warning there would be one every such assembly
// carries for as long as it runs, and an operator who learns to skip it skips
// the ones that mean something along with it. The price is that the one wrong
// declaration of that shape — a module on the endpoint that left its Provides
// empty — is written at Info too: the graph cannot tell it from an absent
// module, and nothing here knows who registered which layer.
//
// A layer that is the only one standing for the capability it names has a
// declaration that places nothing in every assembly, which no configuration
// makes right, so it is written at Warn. So is any cause not named here: a
// level is decided per cause, and one nobody decided is not assumed legal.
func (c unlandedCause) level() slog.Level {
	switch c {
	case causeNoProvider:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

// mount binds the registered routes on the engine.
//
// Whether two routes conflict is gin's judgement, not this module's, and gin
// reports it by panicking as the routes are mounted. Caught here, it becomes a
// startup failure; left alone it would take the process down with a stack
// trace, during a stage whose failures are supposed to abort the startup in an
// orderly way.
//
// What is left to catch here is the clash: every route was offered to an
// engine of its own when it was registered, and one gin refuses by itself
// never reaches this point. Which route the refused one clashes with is found
// by offering pairs; see conflictError.
func (e *endpoint) mount(engine *gin.Engine) error {
	mounted := make([]route, 0, len(e.routes))
	for _, r := range e.routes {
		if raised := offerRoute(engine, r); raised != nil {
			return e.conflictError(mounted, r, raised)
		}
		mounted = append(mounted, r)
	}
	return nil
}

// offerRoute binds one route and hands back whatever gin raised, or nil when it
// took the route.
func offerRoute(engine *gin.Engine, r route) (raised any) {
	defer func() { raised = recover() }()
	engine.Handle(r.method, r.path, r.handlers...)
	return nil
}

// conflictError names both routes and the endpoint they are on.
//
// It names neither registrant: the registration surface does not know who
// called it, which is the same reason a middleware has to declare its own
// Provides. A method and a path are literals in the source, so the two routes
// locate both calls.
//
// The second route is found by offering the refused route and each already
// mounted one, pair by pair, to engines of their own: the pair that reproduces
// the refusal is the clash. It is not read out of what gin raised, because
// that value is gin's own wording and this module does not parse it. A route
// that gin refuses even alone cannot be a partner and is skipped.
func (e *endpoint) conflictError(mounted []route, refused route, raised any) error {
	for _, candidate := range mounted {
		probe := gin.New()
		if offerRoute(probe, candidate) != nil {
			continue
		}
		if offerRoute(probe, refused) != nil {
			return fmt.Errorf("%w: endpoint %q: the routes %q and %q cannot both be mounted: %v. "+
				"Either two registrations claim the same method and path, or the paths clash in "+
				"gin's router: a parameter named differently at one position, or a catch-all "+
				"beside a static segment or a parameter. Change one of them or move it to "+
				"another endpoint", ErrRouteConflict, e.settings.name, candidate.String(),
				refused.String(), raised)
		}
	}
	// No already-mounted route reproduces the refusal in a pair, so the clash
	// is not between two routes alone. Saying so beats naming the one route
	// this module is sure about and letting it read like a verdict about that
	// route alone.
	return fmt.Errorf("%w: endpoint %q: gin refused the route %q beside the %d already mounted "+
		"on this endpoint, and no single one of them reproduces the refusal in a pair, so the "+
		"refusal could not be attributed to a pair of routes: %v. Change the route or move it "+
		"to another endpoint", ErrRouteConflict, e.settings.name, refused.String(), len(mounted), raised)
}

// base is the outermost layer of the chain, the one this module puts there
// itself.
//
// The body limit is imposed here rather than in the handlers, so that it holds
// for every handler on the endpoint, and so that a middleware reading the body
// is already reading a truncated one.
//
// The logger and the delivered ErrorResponder are put into the request context
// here for the same reason of position: outside every layer is the only place
// from which each of them, and the handler, can take them out of the request
// context. First injection belongs to whoever creates the context rather than
// to a middleware — several middlewares would each believe they are the
// fallback, and the loggers they inject carry different module names.
//
// With no Logger capability in the assembly nothing is injected and nothing
// fails: log.FromContext then falls back to the process default logger, so the
// request path's records go to standard output instead of to the configured
// destinations, without a failure signal. That is the stated cost of the
// dependency being optional. With no ErrorResponder, Fail writes the default
// shape.
func (e *endpoint) base(next nethttp.Handler, injected *slog.Logger, responder ErrorResponder) nethttp.Handler {
	limit := e.settings.maxBodyBytes
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if limit > 0 && r.Body != nil {
			r.Body = nethttp.MaxBytesReader(w, r.Body, limit)
		}
		if injected != nil || responder != nil {
			ctx := r.Context()
			if injected != nil {
				ctx = log.WithLogger(ctx, injected)
			}
			r = r.WithContext(withResponder(ctx, responder))
		}
		next.ServeHTTP(w, r)
	})
}

// chainDescription renders an assembled chain for the startup log, outermost
// first.
//
// A registered layer is written down together with the capabilities it stands
// for. This line is where those are readable side by side, and what makes it
// worth reading: a capability no layer on this endpoint stands for is a
// position another layer's After or Before asked for and did not get, and the
// name alone does not answer whether the layer that should have stood for it
// was in the assembly at all. The layers this module puts outside every
// registered one are named too, and by name alone: they are not on the
// registration surface and stand for no capability, so a reader comparing the
// line with the code would otherwise find the body limit, the logger and the
// panic fallback applied by something the line does not mention.
func chainDescription(endpoint string, ordered []layer) string {
	parts := make([]string, 0, len(ordered)+2)
	parts = append(parts, baseName, fallbackName)
	for _, l := range ordered {
		parts = append(parts, chainLayer(endpoint, l))
	}
	return strings.Join(parts, " -> ")
}

// chainLayer names one registered layer on the chain listing, and the
// capabilities it stands for.
//
// A layer whose Provides is empty is written as its name alone. That is legal
// — nobody can point at such a layer, which is the whole cost — and it reads
// the same way the built-in layers do, which is how a reader tells a layer
// that stands for nothing from one whose capabilities are listed.
func chainLayer(endpoint string, l layer) string {
	if len(l.provides) == 0 {
		return l.mw.Name
	}
	return l.mw.Name + "(" + strings.Join(capabilityNames(endpoint, l, "Provides", l.provides), ", ") + ")"
}
