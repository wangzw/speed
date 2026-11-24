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

func (e *endpoint) assemble(injected, logger *slog.Logger, responder ErrorResponder, localizer i18n.Localizer, resolver i18n.Resolver) (nethttp.Handler, error) {
	ordered, remarks, err := orderLayers(e.settings.name, e.layers)
	if err != nil {
		return nil, err
	}

	engine := newEngine(e.settings, localizer)
	// ordered runs from outermost to innermost, and gin runs the handlers given to Use in
	// the order given. Use goes first: gin combines the engine's handlers into a route's
	// chain when the route is added, so a layer registered afterwards would not reach it.
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

func claimSummary(prefixes []string) string {
	return "[" + strings.Join(prefixes, ", ") + "]"
}

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

var ginMode sync.Once

func setReleaseMode() {
	ginMode.Do(func() { gin.SetMode(gin.ReleaseMode) })
}

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

const heldAllowKey = "http:held-allow"

// holdAllowName is holdAllow as it appears in the assembled chain's description.
// It is not one of the names the chain renderer would otherwise invent for a
// registered layer, because holdAllow is on no registration surface.
const holdAllowName = "holdAllow"

// holdAllow is the first handler on every engine: gin answers a request whose method the path does not allow with the Allow header already on the response, so this takes it off and keeps it for the method-not-allowed handler, and a layer refusing first does not carry it out.
func holdAllow(c *gin.Context) {
	if allow := c.Writer.Header().Get("Allow"); allow != "" {
		c.Writer.Header().Del("Allow")
		c.Set(heldAllowKey, allow)
	}
	c.Next()
}

const unlandedMsg = "an HTTP middleware constraint landed on no layer"

const stageSatisfiedMsg = "an HTTP middleware constraint is already given by the stage order"

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

func (c remarkCause) message() string {
	switch c {
	case causeStageSatisfied:
		return stageSatisfiedMsg
	default:
		return unlandedMsg
	}
}

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

func offerRoute(engine *gin.Engine, r route) (raised any) {
	defer func() { raised = recover() }()
	engine.Handle(r.method, r.path, r.handlers...)
	return nil
}

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
	// No already-mounted route reproduces the refusal in a pair, so the clash is not
	// between two routes alone.
	return fmt.Errorf("%w: endpoint %q: gin refused the route %q beside the %d already mounted "+
		"on this endpoint, and no single one of them reproduces the refusal in a pair, so the "+
		"refusal could not be attributed to a pair of routes: %v. Change the route or move it "+
		"to another endpoint", ErrRouteConflict, e.settings.name, refused.String(), len(mounted), raised)
}

func (e *endpoint) base(next nethttp.Handler, injected *slog.Logger, responder ErrorResponder, resolver i18n.Resolver) nethttp.Handler {
	return injectContext(next, injected, responder, resolver)
}

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

const contextName = "http:context"

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

func chainLayer(endpoint string, l layer) string {
	if len(l.provides) == 0 {
		return l.mw.Name
	}
	return l.mw.Name + "(" + strings.Join(capabilityNames(endpoint, l, "Provides", l.provides), ", ") + ")"
}
