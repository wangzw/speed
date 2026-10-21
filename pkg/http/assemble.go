package http

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

// assemble builds this endpoint's final handler, once, from the inside out: a
// gin engine is made, the layers are handed to its Use in the order the stages,
// the constraints and the Orders give — with the built-in access log, panic
// recovery and body limit placed between the stages they belong between — the
// routes are mounted on it, and the base sits outside all of it.
//
// injected is the logger to put in each request's context, or nil when no
// module delivers the Logger capability. logger is where this assembly's own
// diagnostics go. responder is what decides the shape of the error responses,
// or nil when no module delivers the ErrorResponder capability. localizer
// renders the text of the refusals this module writes itself, or nil when no
// module delivers the Localizer capability. resolver names the language of the
// default problem shape in a Content-Language header, or nil when no module
// delivers the Resolver capability.
func (e *endpoint) assemble(injected, logger *slog.Logger, responder ErrorResponder, localizer i18n.Localizer, resolver i18n.Resolver) (nethttp.Handler, error) {
	ordered, remarks, err := orderLayers(e.settings.name, e.layers)
	if err != nil {
		return nil, err
	}

	engine := newEngine(e.settings, localizer)
	// ordered runs from outermost to innermost, and gin runs the handlers
	// given to Use in the order given. Use goes first: gin combines the
	// engine's handlers into a route's chain when the route is added, so a
	// layer registered after the routes would not reach them.
	if raised := useLayers(engine, ordered, e.settings, localizer); raised != nil {
		return nil, fmt.Errorf("%w: endpoint %q: gin refused the %d registered layers: %v. A chain "+
			"holds a limited number of handlers, and the layers, the engine's own and a route's "+
			"count together; register fewer layers on this endpoint or move some of them to "+
			"another endpoint", ErrRouteConflict, e.settings.name, len(ordered), raised)
	}
	if err := e.mount(engine); err != nil {
		return nil, err
	}

	reportRemarks(logger, remarks)
	logger.Info("an HTTP endpoint's chain is assembled",
		"endpoint", e.settings.name,
		"routes", len(e.routes),
		"prefixes", claimSummary(e.settings.prefixes),
		"chain", chainDescription(e.settings.name, ordered))
	return e.base(engine, injected, responder, resolver), nil
}

// claimSummary renders an endpoint's claimed prefixes for the startup line, for
// example [/api, /]. It carries the claims beside the route count so an
// operator can check that a path is answered by the endpoint they meant, and a
// differential test can confirm an endpoint really lost its routes while
// keeping its claim.
func claimSummary(prefixes []string) string {
	return "[" + strings.Join(prefixes, ", ") + "]"
}

// useLayers registers this module's built-in layers and the ordered registered
// ones on the engine, outermost first, and returns what gin raised, or nil.
//
// The built-ins are placed between the stages, which is what the stage order
// cannot express on its own, and each is placed against a contract:
//
//   - the access log sits outside the observe stage, so it sees every request,
//     including the ones the observe stage's own layers never hand on;
//   - the panic recovery sits inside the observe stage and outside the guard
//     stage, so an observer sees a recovered request as the 500 that was
//     written rather than as a panic thrown past it;
//   - the request body limit sits inside the guard stage and outside every
//     stage below it — it is registered after every guard layer and before the
//     identify stage — so a body is already bounded when an identify, throttle,
//     record or authorize layer, or the handler, reads it.
//
// The body limit's place is the guard contract and not an accident. A guard
// layer refuses on what it can see without reading the body — an address, a
// header, a CORS preflight — so it neither needs a bounded body nor may have
// one imposed on it: bounding first would answer an oversize request before the
// address limit had counted it. It is also the one boundary that keeps the CORS
// rule: a 413 is a refusal, so the layer that writes it has to sit inside CORS,
// and CORS is a guard layer. Put the body limit outside the guard stage and the
// 413 goes out without the Access-Control-Allow-Origin a browser needs to read
// it.
//
// None of the built-ins is registered through the surface, so none is named by
// a constraint and none is moved by one.
//
// gin refuses a chain longer than it can hold by panicking, and that would
// otherwise leave Serve as a panic instead of a startup failure, which is why
// what it raised is caught and handed back.
func useLayers(engine *gin.Engine, ordered []layer, s endpointSettings, localizer i18n.Localizer) (raised any) {
	defer func() { raised = recover() }()
	engine.Use(accessLog(s.name))
	observe, guard, inner := splitStages(ordered)
	for _, l := range observe {
		engine.Use(l.mw.Handler)
	}
	engine.Use(recovery(s.name, localizer))
	for _, l := range guard {
		engine.Use(l.mw.Handler)
	}
	engine.Use(bodyLimit(s.name, s.maxBodyBytes, localizer))
	for _, l := range inner {
		engine.Use(l.mw.Handler)
	}
	return nil
}

// splitStages divides the ordered layers into the observe stage, the guard
// stage, and everything inside the guard stage, each keeping its order.
//
// The two boundaries are the points the recovery and the body limit sit at, and
// they are read off the stage and nothing else: orderLayers runs the stages in
// their declared order, so the observe layers come out first and the guard ones
// next, but the partition is made by the stage each layer declared rather than
// by its position in the slice, so a change to the built-in order here cannot
// put an outer or an inner layer on the wrong side of a boundary in silence.
func splitStages(ordered []layer) (observe, guard, inner []layer) {
	observe = make([]layer, 0, len(ordered))
	guard = make([]layer, 0, len(ordered))
	inner = make([]layer, 0, len(ordered))
	for _, l := range ordered {
		switch l.mw.Stage {
		case StageObserve:
			observe = append(observe, l)
		case StageGuard:
			guard = append(guard, l)
		default:
			inner = append(inner, l)
		}
	}
	return observe, guard, inner
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
// answered through Fail.
//
// The engine trusts no proxy unless the endpoint's configuration names the
// networks in front of it, on the endpoint itself or in the module-level default
// it inherits when it writes none, in which case the client address
// Context.ClientIP reports is taken from the forwarded-address headers named
// there. The default is no trust: gin's own default trusts any peer, which lets
// a client put whatever address it likes in X-Forwarded-For and choose the
// address the rate limiter keys its buckets by and a session records. The
// trusted networks are handed to gin's SetTrustedProxies and the header names
// to its RemoteIPHeaders, so this module reads no forwarded header itself;
// configuration has already refused a network gin cannot parse, which is why
// SetTrustedProxies cannot fail here. Context falls back to the request's
// context, so a logger put there is reachable from the gin context as well.
//
// gin writes the Allow header of a 405 into the response before it runs any
// layer, so a layer that refuses such a request — authentication with a 401,
// tenant resolution with a 404, rate limiting with a 429 — would send the
// caller the fact that the path exists and the methods it allows. The first
// handler on the engine takes the header off and holds it; the handler for a
// method the path does not allow puts it back once every layer has let the
// request through.
func newEngine(s endpointSettings, localizer i18n.Localizer) *gin.Engine {
	setReleaseMode()
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.HandleMethodNotAllowed = true
	engine.ContextWithFallback = true
	_ = engine.SetTrustedProxies(s.trustedProxies)
	if len(s.remoteIPHeaders) > 0 {
		engine.RemoteIPHeaders = s.remoteIPHeaders
	}
	engine.Use(holdAllow)
	engine.NoRoute(func(c *gin.Context) {
		Fail(c, Problem{
			Status: nethttp.StatusNotFound,
			Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusNotFound),
		})
	})
	engine.NoMethod(func(c *gin.Context) {
		if allow, held := c.Get(heldAllowKey); held {
			if methods, isString := allow.(string); isString {
				c.Header("Allow", methods)
			}
		}
		Fail(c, Problem{
			Status: nethttp.StatusMethodNotAllowed,
			Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusMethodNotAllowed),
		})
	})
	return engine
}

// heldAllowKey is the key holdAllow keeps the Allow header of a 405 under in
// the gin context.
const heldAllowKey = "http:held-allow"

// holdAllowName is holdAllow as it appears in the assembled chain's description.
// It is not one of the names the chain renderer would otherwise invent for a
// registered layer, because holdAllow is on no registration surface.
const holdAllowName = "holdAllow"

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
// nothing: the two causes where a layer the constraint named is one the
// ordering could not use. It is shared, because the two are answered by the
// same family of edit — a layer is missing or a declaration is wrong — and the
// cause attribute tells the reader which of the two it is.
const unlandedMsg = "an HTTP middleware constraint landed on no layer"

// stageSatisfiedMsg is the message of the record reporting a constraint whose
// provider is in another stage and in the direction the stage order already
// gives. Nothing is wrong here and nothing is missing: the constraint repeats
// the boundary, which is why the record is Debug and not one of the two below.
const stageSatisfiedMsg = "an HTTP middleware constraint is already given by the stage order"

// reportRemarks writes out what the ordering noticed and did not act on, one
// record each, at the level and in the words its cause calls for.
//
// This is the whole of their observability. The assembly carries on — a
// capability absent from the process is a legal configuration — so the layer
// that declared itself inside authentication runs anyway, in the place its
// stage and Order happen to give it. The cause is written with each line
// because the causes are answered by different edits: a capability nobody
// stands for sends the reader looking for the module that should have been on
// the endpoint; a capability only the declaring layer stands for is that layer
// correcting what it said about itself; a constraint the stage order already
// satisfies needs nothing done at all.
//
// It goes to this module's own logger because core does not export a startup
// diagnostics surface. The level is the cause's; see remarkCause.level.
func reportRemarks(logger *slog.Logger, remarks []orderingRemark) {
	for _, u := range remarks {
		logger.Log(context.Background(), u.cause.level(), u.cause.message(),
			"endpoint", u.endpoint,
			"middleware", u.layer,
			"declaration", fmt.Sprintf("%s[%d]", u.field, u.at),
			"capability", u.capability,
			"cause", string(u.cause))
	}
}

// message is the text a remark is written with. Each cause reads differently
// because each sends its reader somewhere else: the two unplaced causes to the
// absent module or the wrong declaration, and the stage-satisfied one to the
// stage boundary that already holds.
func (c remarkCause) message() string {
	switch c {
	case causeStageSatisfied:
		return stageSatisfiedMsg
	default:
		return unlandedMsg
	}
}

// level is the level a remark is written at. What decides it is whether the
// assembly can be right as it stands.
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
//
// A constraint the stage order already satisfies is written at Debug. The
// boundary places the layer whatever the constraint says, so nothing is wrong
// and nothing is missing — the declaration only repeats what the stages
// already give, and a reader chasing their After/Before chain is the one who
// wants to see it.
func (c remarkCause) level() slog.Level {
	switch c {
	case causeStageSatisfied:
		return slog.LevelDebug
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
// It puts the logger, the delivered ErrorResponder and the delivered Resolver
// into the request context and nothing else. Outside every layer is the only
// place from which each of them, and the handler, can take them out of the
// request context. First
// injection belongs to whoever creates the context rather than to a middleware —
// several middlewares would each believe they are the outermost one, and the
// loggers they inject carry different module names.
//
// With no Logger capability in the assembly nothing is injected and nothing
// fails: log.FromContext then falls back to the process default logger, so the
// request path's records go to standard output instead of to the configured
// destinations, without a failure signal. That is the stated cost of the
// dependency being optional. With no ErrorResponder, Fail writes the default
// shape. With no Resolver the default shape names no language; the four
// refusals are still written.
//
// The request body limit is not imposed here: it is the body limit layer's, and
// that layer sits inside the engine, after the route is matched, so a request
// whose method the path does not allow and one that matches no route are bounded
// too.
func (e *endpoint) base(next nethttp.Handler, injected *slog.Logger, responder ErrorResponder, resolver i18n.Resolver) nethttp.Handler {
	return injectContext(next, injected, responder, resolver)
}

// injectContext wraps next so that every request reaching it carries the
// injected logger, the delivered ErrorResponder and the delivered Resolver in
// its context. An endpoint puts it around its own engine; a listener puts it
// around its dispatch, so that a listener-level 404 or 405 is written through
// the same shape and the same resolver as an endpoint's own.
//
// With no Logger capability in the assembly nothing is injected and nothing
// fails. With no ErrorResponder, Fail writes the default shape. With no Resolver
// the default shape names no language. Any of the three being absent leaves the
// others in place.
func injectContext(next nethttp.Handler, injected *slog.Logger, responder ErrorResponder, resolver i18n.Resolver) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if injected != nil || responder != nil || resolver != nil {
			ctx := r.Context()
			if injected != nil {
				ctx = log.WithLogger(ctx, injected)
			}
			ctx = withResolver(withResponder(ctx, responder), resolver)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// contextName is the outermost built-in layer — the one that injects the logger
// and the ErrorResponder — as it appears in the assembled chain's description.
const contextName = "http:context"

// chainDescription renders an assembled chain for the startup log, outermost
// first.
//
// The built-in layers are named where they sit: the context and holdAllow
// outside every registered layer, the access log outside the observe stage, the
// panic recovery between the observe stage and the guard stage, and the request
// body limit between the guard stage and the rest. None of them is on the
// registration surface, so a reader comparing the line with the code would
// otherwise find work applied by something the line does not mention, and the
// reader would not see that the recovery sits inside the observers, which is
// the property the whole order exists for. The body limit after the guard's
// layers is what shows a 413 going out inside CORS, which is a guard layer.
//
// Each stage is written as a marker followed by its layers, in the order the
// chain runs them, so the category constraint — every observer outside every
// layer that can refuse — is readable off the line. A registered layer is
// written down together with the capabilities it stands for: a capability no
// layer on this endpoint stands for is a position another layer's After or
// Before asked for and did not get, and the name alone does not answer whether
// the layer that should have stood for it was in the assembly at all.
func chainDescription(endpoint string, ordered []layer) string {
	parts := make([]string, 0, len(ordered)+6)
	parts = append(parts, contextName, holdAllowName, accessLogName)
	parts = append(parts, chainStage(endpoint, ordered, StageObserve)...)
	parts = append(parts, recoveryName)
	parts = append(parts, chainStage(endpoint, ordered, StageGuard)...)
	parts = append(parts, bodyLimitName)
	for _, st := range declaredStages {
		if st == StageObserve || st == StageGuard {
			continue
		}
		parts = append(parts, chainStage(endpoint, ordered, st)...)
	}
	return strings.Join(parts, " -> ")
}

// chainStage renders the layers of one stage, each keeping its order. The
// stage marker is written against the first layer of the stage, joined to it by
// a space rather than by the arrow between layers, so a marker and its layers
// read as one group — "[observe] otel -> otel:guard" — and the marker names the
// category each of the layers after it belongs to.
func chainStage(endpoint string, ordered []layer, st Stage) []string {
	var out []string
	for _, l := range ordered {
		if l.mw.Stage != st {
			continue
		}
		name := chainLayer(endpoint, l)
		if len(out) == 0 {
			name = "[" + stageName(st) + "] " + name
		}
		out = append(out, name)
	}
	return out
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
