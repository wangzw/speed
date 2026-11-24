package preferences

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/notification"
)

// TestTheModuleMountsOnlyOnTheEndpointsItNames holds that the preference
// routes go on the endpoints the module is told to and on none else.
func TestTheModuleMountsOnlyOnTheEndpointsItNames(t *testing.T) {
	api := &stubEndpoint{}
	covered := newCoveredEndpoint()
	router := newMixedRouter()
	router.declare("api", api)
	router.declare("web", covered)

	h := engines[0].on(t, harness{withRouter: true, router: router, endpoints: []string{"api"}})
	h.drive(t, func(_ context.Context, _ *core.Registry) error {
		got := api.registered()
		if len(got) != len(wantRoutes) {
			t.Fatalf("the named endpoint mounted %d routes (%v), want %d", len(got), got, len(wantRoutes))
		}
		for _, r := range got {
			if !wantRoutes[r] {
				t.Errorf("unexpected route %s %s", r.method, r.path)
			}
		}
		if elsewhere := covered.registered(); len(elsewhere) != 0 {
			t.Errorf("an endpoint the module was not told to carry the routes registered %v, want none",
				elsewhere)
		}
		if refused := covered.refusedRoutes(); len(refused) != 0 {
			t.Errorf("the routes %v clashed with the catch-all on an endpoint the module was not told "+
				"to carry them", refused)
		}
		return nil
	})
}

// With the entry point present, an empty endpoint list fails the startup naming
// the key: a run that mounts the routes has to say where.
func TestAnEmptyEndpointListWithARouterFailsInit(t *testing.T) {
	h := engines[0].on(t, harness{withRouter: true, router: newStubRouter("public"), endpoints: []string{}})
	err := h.run(t, func(context.Context, *core.Registry) error { return nil })
	if !errors.Is(err, notification.ErrInvalidConfig) {
		t.Fatalf("the drive ended with %v, want notification.ErrInvalidConfig", err)
	}
	if err != nil && !strings.Contains(err.Error(), endpointsKey) {
		t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
	}
}

// The endpoint list refuses a shape the routes cannot be mounted by, naming the
// key, when the routes are mounted.
func TestTheEndpointListRefusesBadShapes(t *testing.T) {
	cases := map[string][]string{
		"star":       {"*"},
		"empty name": {""},
		"duplicate":  {"public", "public"},
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			h := engines[0].on(t, harness{
				withRouter: true, router: newStubRouter("public"), endpoints: endpoints,
			})
			err := h.run(t, func(context.Context, *core.Registry) error { return nil })
			if !errors.Is(err, notification.ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want notification.ErrInvalidConfig", err)
			}
			if err != nil && !strings.Contains(err.Error(), endpointsKey) {
				t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
			}
		})
	}
}

// A name the entry point does not declare fails the startup naming the key and
// the endpoint, rather than leaving the routes silently unmounted.
func TestAnUndeclaredEndpointFailsInit(t *testing.T) {
	h := engines[0].on(t, harness{
		withRouter: true, router: newStubRouter("public"), endpoints: []string{"admin"},
	})
	err := h.run(t, func(context.Context, *core.Registry) error { return nil })
	if err == nil {
		t.Fatal("the drive came up with an endpoint the entry point does not declare")
	}
	for _, want := range []string{endpointsKey, "admin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %q: %v", want, err)
		}
	}
}

// With no entry point a malformed endpoint list is not read and the module still
// comes up: the list is judged only where the routes are mounted.
func TestAMalformedEndpointListIsNotReadWithoutARouter(t *testing.T) {
	h := engines[0].on(t, harness{endpoints: []string{"*", ""}})
	if err := h.run(t, func(context.Context, *core.Registry) error { return nil }); err != nil {
		t.Fatalf("the drive failed with an unused malformed endpoint list: %v", err)
	}
}

type mixedRouter struct {
	mu        sync.Mutex
	endpoints map[string]speedhttp.Endpoint
	order     []string
}

func newMixedRouter() *mixedRouter {
	return &mixedRouter{endpoints: map[string]speedhttp.Endpoint{}}
}

func (r *mixedRouter) declare(name string, e speedhttp.Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints[name] = e
	r.order = append(r.order, name)
}

func (r *mixedRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, ok := r.endpoints[name]
	if !ok {
		return nil, errors.New("the entry point declares no endpoint " + name)
	}
	return endpoint, nil
}

func (r *mixedRouter) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

type coveredEndpoint struct {
	mu      sync.Mutex
	engine  *gin.Engine
	routes  []mountedRoute
	refused []mountedRoute
}

func newCoveredEndpoint() *coveredEndpoint {
	gin.SetMode(gin.TestMode)
	e := &coveredEndpoint{engine: gin.New()}
	e.engine.Handle(http.MethodGet, "/*path", func(*gin.Context) {})
	return e
}

func (e *coveredEndpoint) Route(method, path string, handlers ...gin.HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if raised := offerRoute(e.engine, method, path, handlers); raised != nil {
		e.refused = append(e.refused, mountedRoute{method, path})
		return
	}
	e.routes = append(e.routes, mountedRoute{method, path})
}

func offerRoute(engine *gin.Engine, method, path string, handlers []gin.HandlerFunc) (raised any) {
	defer func() { raised = recover() }()
	engine.Handle(method, path, handlers...)
	return nil
}

func (e *coveredEndpoint) Use(speedhttp.Middleware) {}

func (e *coveredEndpoint) Accepting() bool { return true }

func (e *coveredEndpoint) registered() []mountedRoute {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]mountedRoute(nil), e.routes...)
}

func (e *coveredEndpoint) refusedRoutes() []mountedRoute {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]mountedRoute(nil), e.refused...)
}
