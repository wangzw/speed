// Package resolve decides which tenant a request belongs to: it reads a name off
// the request with the configured extractor, looks it up in the roster and
// writes the tenant's identifier into the request context.
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

// ConfigNamespace is where this module's input items are mounted; it equals
// ModuleName.
const ConfigNamespace = ModuleName

const diagnosticPrefix = "tenant: "

var contextLogger = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers tenant.Resolution
// (exclusively), speedhttp.RouteGuard and speedhttp.TenantAdminRouteGuard, and
// requires tenant.Catalog.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			// Requiring the roster orders its Init, which loads the snapshot, before this
			// module's Init, which registers the layer.
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

		// The registration window opens in Migrate and closes in Start so that it
		// spans the whole Init stage; do not move it into this module's Init.
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

	// selected is written in seal and read by request goroutines afterwards
	// without a lock.
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

// Register adds an extractor under name. It is accepted from this module's
// Migrate callback until its Start callback and panics outside that window; a
// malformed or duplicate registration is recorded and reported when the window
// closes.
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

// middlewareFor places the layer inside authentication: a request the gate
// refused never reaches it, and exempt routes still run it.
func (r *resolver) middlewareFor() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*tenant.Resolution)(nil)},
		Stage:    speedhttp.StageIdentify,
		After:    []core.Token{(*authn.Gate)(nil)},
		Handler:  r.handle,
	}
}

// Guard wraps one route of a tenant-scoped group so that the tenant its path
// names is the tenant the request resolved to.
func (r *resolver) Guard(next gin.HandlerFunc) gin.HandlerFunc {
	return pathguard.New(r.roster).Guard(next)
}

// GuardTenantAdmin wraps one route of a tenant management surface with the same
// check and a refusal of the platform domain.
func (r *resolver) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	return pathguard.NewTenantAdmin(r.roster).GuardTenantAdmin(next)
}

func (r *resolver) handle(c *gin.Context) {
	if !r.admit(c) {
		return
	}
	c.Next()
}

// admit resolves one request and puts the tenant into its context; false means
// a response was written through speedhttp.Fail. A panic here is recovered and
// answered with a 500, but the call to the next layer stays outside it: a
// handler's panic is not this layer's to catch.
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

// lookUp looks the name up as a tenant name only: an identifier sent as a name
// is refused like any unknown name.
func (r *resolver) lookUp(ctx context.Context, name string) (tenant.Tenant, bool, error) {
	return r.roster.ByName(ctx, name)
}

// refuse answers a bare 404 and logs the reason through the request's own
// logger, not the one this module holds.
func (r *resolver) refuse(c *gin.Context, why, name string) {
	attrs := []any{"path", c.Request.URL.Path, "reason", why}
	if name != "" {
		attrs = append(attrs, "name", name)
	}
	log.FromContext(c.Request.Context()).Info(diagnosticPrefix+ModuleName+": request refused", attrs...)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

// checkExtractorName refuses an empty name and one with any character
// unicode.IsSpace counts as space.
func checkExtractorName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: the name is empty", tenant.ErrInvalidExtractor)
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: %q carries a blank", tenant.ErrInvalidExtractor, name)
	}
	return nil
}

// isNilExtractor also catches an interface holding a typed nil, which is not
// equal to nil but panics on the first call.
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
