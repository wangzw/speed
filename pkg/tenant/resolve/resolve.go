// Package resolve carries the layer that decides which tenant a request belongs to: it reads a name
// with the configured extractor, resolves it through the roster, and writes the identifier into the
// request context. Registering it on endpoints is what decides that a binary resolves tenants.
package resolve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
	"github.com/vislake/speed/pkg/tenant/pathguard"
)

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.resolve"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const diagnosticPrefix = "tenant: "

var contextLogger = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers the resolving layer as tenant.Resolution,
// exclusively, and the two route guards as speedhttp.RouteGuard and speedhttp.TenantAdminRouteGuard. The
// roster is required and must be loaded before this module registers its layer.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*tenant.Resolution)(nil), Exclusive: true},
			{Token: (*speedhttp.RouteGuard)(nil), Exclusive: true},
			{Token: (*speedhttp.TenantAdminRouteGuard)(nil), Exclusive: true},
		},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return prepare(reader)
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			set, err := readSettings(reader)
			if err != nil {
				return nil, err
			}
			catalog, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return nil, err
			}
			return newResolver(set, catalog), nil
		},

		// The window opens in Migrate and shuts in Start, spanning the whole Init
		// stage, because a module registering an extractor declares no dependency
		// on this one and may register from an Init that runs first.
		Migrate: func(_ context.Context, _ *core.Registry, instance any) error {
			if r, ok := productOf(instance); ok {
				r.open()
			}
			return nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			return r.wire(reg)
		},

		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			return r.seal()
		},
	}
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	return core.Resolve[config.Reader](reg)
}

func productOf(instance any) (*resolver, bool) {
	r, ok := instance.(*resolver)
	return r, ok
}

type resolver struct {
	settings settings
	roster   tenant.Catalog

	mu         sync.Mutex
	openWindow bool
	extractors map[string]tenant.Extractor
	faults     []error

	selected tenant.Extractor
	guarded  []string
	logger   *slog.Logger
}

var (
	_ speedhttp.RouteGuard            = (*resolver)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*resolver)(nil)
)

func newResolver(set settings, roster tenant.Catalog) *resolver {
	return &resolver{
		settings:   set,
		roster:     roster,
		extractors: make(map[string]tenant.Extractor),
		logger:     log.Default(),
	}
}

func (r *resolver) open() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openWindow = true
}

// Register implements tenant.Resolution. A call outside the registration window
// panics. A malformed registration or a duplicate name is recorded rather than
// raised, because the window has to stay open; seal reports them once it closes.
func (r *resolver) Register(name string, e tenant.Extractor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.openWindow {
		panic(ModuleName + ": an extractor was registered outside the registration window. " +
			"The surface accepts registrations from this module's Migrate callback until its " +
			"Start callback, a window that contains the whole Init stage. Register the " +
			"extractor from the module's Init callback")
	}
	if err := checkExtractorName(name); err != nil {
		r.faults = append(r.faults, err)
		return
	}
	if isNilExtractor(e) {
		r.faults = append(r.faults, fmt.Errorf("%w: %q has no implementation",
			tenant.ErrInvalidExtractor, name))
		return
	}
	if prev, taken := r.extractors[name]; taken {
		r.faults = append(r.faults, fmt.Errorf("%w: %q is registered twice, as %T and %T",
			tenant.ErrExtractorConflict, name, prev, e))
		return
	}
	r.extractors[name] = e
}

func (r *resolver) wire(reg *core.Registry) error {
	r.registerBuiltins()
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		r.logger = delivered.Named(ModuleName)
	}
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, tenant.ErrNoRouter)
	}
	for _, name := range r.settings.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s%s: %w: %q: %w", diagnosticPrefix, ModuleName,
				tenant.ErrUnknownEndpoint, name, err)
		}
		endpoint.Use(r.middlewareFor())
		r.guarded = append(r.guarded, name)
	}
	return nil
}

func (r *resolver) registerBuiltins() {
	r.Register(HeaderExtractor, headerExtractor{name: r.settings.header})
	r.Register(SubdomainExtractor, subdomainExtractor{index: r.settings.subdomain})
	r.Register(PathExtractor, pathExtractor{index: r.settings.path})
}

func (r *resolver) seal() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openWindow = false
	if len(r.faults) > 0 {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, errors.Join(r.faults...))
	}
	chosen, ok := r.extractors[r.settings.extractor]
	if !ok {
		return fmt.Errorf("%s%s: %w: %q is not among the registered ones (%s)",
			diagnosticPrefix, ModuleName, tenant.ErrNoExtractor,
			r.settings.extractor, registeredNames(r.extractors))
	}
	r.selected = chosen
	r.logger.Info(diagnosticPrefix+ModuleName+": resolution in force",
		"extractor", r.settings.extractor, "guarded", r.guarded)
	return nil
}

func (r *resolver) middlewareFor() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*tenant.Resolution)(nil)},
		Stage:    speedhttp.StageIdentify,
		After:    []core.Token{(*authn.Gate)(nil)},
		Handler:  r.handle,
	}
}

// Guard implements speedhttp.RouteGuard: it holds a tenant-scoped route's path tenant to the tenant
// the request resolved to. It is a capability rather than an endpoint layer because platform routes
// on the same endpoint name a tenant as data and must not be held to this rule.
func (r *resolver) Guard(next gin.HandlerFunc) gin.HandlerFunc {
	return pathguard.New(r.roster).Guard(next)
}

// GuardTenantAdmin implements speedhttp.TenantAdminRouteGuard: the identity
// rule plus the refusal of the platform domain, which only the tenant
// administration surfaces apply.
func (r *resolver) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	return pathguard.NewTenantAdmin(r.roster).GuardTenantAdmin(next)
}

func (r *resolver) handle(c *gin.Context) {
	if !r.admit(c) {
		return
	}
	c.Next()
}

func (r *resolver) admit(c *gin.Context) (admitted bool) {
	req := c.Request
	defer func() {
		if rec := recover(); rec != nil {
			log.FromContext(req.Context()).Error(diagnosticPrefix+ModuleName+
				": the resolution layer panicked while resolving this request",
				slog.Any("panic", rec))
			speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusInternalServerError})
			admitted = false
		}
	}()

	name, ok := r.selected.Extract(req)
	if !ok {
		r.refuse(c, "the request carries no tenant name", "")
		return false
	}
	found, present, err := r.lookUp(req.Context(), name)
	if err != nil {
		log.FromContext(req.Context()).Error(diagnosticPrefix+ModuleName+
			": the roster could not be read", "error", err)
		speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusInternalServerError})
		return false
	}
	if !present {
		r.refuse(c, "the name is not on the roster", name)
		return false
	}
	if found.Status != tenant.StatusActive {
		r.refuse(c, "the tenant is not being served", name)
		return false
	}
	c.Request = req.WithContext(tenantctx.Bind(req.Context(), string(found.ID), contextLogger))
	return true
}

func (r *resolver) lookUp(ctx context.Context, name string) (tenant.Tenant, bool, error) {
	return r.roster.ByName(ctx, name)
}

func (r *resolver) refuse(c *gin.Context, why, name string) {
	attrs := []any{"path", c.Request.URL.Path, "reason", why}
	if name != "" {
		attrs = append(attrs, "name", name)
	}
	log.FromContext(c.Request.Context()).Info(diagnosticPrefix+ModuleName+": request refused", attrs...)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

func checkExtractorName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: the name is empty", tenant.ErrInvalidExtractor)
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: %q carries a blank", tenant.ErrInvalidExtractor, name)
	}
	return nil
}

func isNilExtractor(e tenant.Extractor) bool {
	if e == nil {
		return true
	}
	v := reflect.ValueOf(e)
	switch v.Kind() {
	case reflect.Pointer, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

func registeredNames(m map[string]tenant.Extractor) string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
