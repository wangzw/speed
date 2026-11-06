// Package resolve carries the layer that decides which tenant a request
// belongs to.
//
// It reads a name off the request surface with the extractor the configuration
// selects, hands that name to the roster, and writes the identifier it comes
// back with into the request context. Everything downstream — handlers and the
// statement filter alike — reads that identifier and nothing else.
//
// It is a module rather than part of the interface package because registering
// a layer on listening endpoints is what decides that a binary resolves
// tenants at all, and a binary that only reads an identifier should not have to
// carry one.
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

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose requests resolve to a tenant.
const ModuleName = "tenant.resolve"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, the way a registering module's namespace always is.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers. It names the capability directory
// rather than the module, and the module follows it in the line itself.
const diagnosticPrefix = "tenant: "

// contextLogger is how the resolved identifier reaches the logger a request's
// context carries.
//
// The field it is bound under is this capability directory's to choose — the
// logging module knows no domain fields — and it is a contract once a
// dashboard reads it. The roster binds the same field when a path enters a
// tenant, so the name is the one both writers share, tenantctx.LogField,
// rather than one fixed here.
var contextLogger = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

// The module registers itself. Importing this package is what puts tenant
// resolution in a binary's available set; a host that never imports it carries
// no resolution layer, whatever else of this capability it takes in.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The module delivers three capabilities. The resolving layer is delivered as
// tenant.Resolution, exclusively: two resolving layers on one endpoint would
// each decide the same request, and which of them wrote the identifier
// downstream reads is not something either could answer — so the pair is
// deliberately unrepresentable, and a second one fails the startup instead. The
// same product delivers speedhttp.RouteGuard, the path-tenant identity guard a
// mounting module wraps a tenant-scoped group's routes with, and
// speedhttp.TenantAdminRouteGuard, the same rule plus the platform-domain
// refusal the tenant management surfaces add. They are two capabilities because
// the refusal is not the shared rule: the members, invitations and SSO groups
// manage the system tenant and are addressed by the platform domain
// legitimately. Both are here because this module already holds the roster the
// guard resolves the path segment through, so a mounting module never takes the
// tenant directory into its own closure.
//
// The roster capability is a construction-time dependency, and here that
// declaration carries an ordering that is not decoration. The roster loads its
// snapshot in Init, this module registers its layer in Init, and Init runs in
// dependency order: without the declaration the two could run either way round,
// and a layer registered before the snapshot exists would refuse every request
// on an endpoint it guards, with nothing saying why.
//
// The declaration does not take the roster in, though. The roster delivers its
// capability exclusively, and the dependency closure never pulls an exclusive
// provider into a command on its own, so a host that writes this module's name
// and not the roster's, and supplies no instance for the roster either, is left
// with no provider for the capability this module requires — which ends the run
// at dependency resolution, before anything is constructed. The two module names
// go into the command together.
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

		// The window is opened in Migrate and shut in Start, the two stage
		// slots the design's stage table gives it. The window that leaves open
		// contains the Init stage whole and reaches beyond it at both ends,
		// because a stage advances one module at a time: this module's Migrate
		// callback runs before the assembly enters Init, and its Start callback
		// runs once Init has ended. Neither end could be this module's own Init
		// — a module registering an extractor declares no dependency on this
		// one, so it may legally register from an Init that runs first.
		//
		// A drive stopping exactly at Migrate opens the window with nobody to
		// shut it. That run registers nothing and serves nothing, and the way
		// round it is for a command whose purpose is to apply migrations not
		// to take this module in.
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

// readerOf takes up the configuration reader. A reader that is not there means
// the host never supplied one as the instance of the config module, which is the
// host's omission and is carried out as it stands rather than read as "nothing
// was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	return core.Resolve[config.Reader](reg)
}

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for this module, and the callbacks leave it
// alone: no registration window is opened or sealed for it and no layer is
// registered on any endpoint, and what it holds is released by whoever supplied
// it.
func productOf(instance any) (*resolver, bool) {
	r, ok := instance.(*resolver)
	return r, ok
}

// resolver is this module's product. It implements tenant.Resolution.
type resolver struct {
	settings settings
	roster   tenant.Catalog

	// mu guards the registration window and what is registered through it.
	// Both are written from other modules' callbacks, which may register
	// from a goroutine of their own, so the order of the writes is not that
	// of the drive.
	mu         sync.Mutex
	openWindow bool
	extractors map[string]tenant.Extractor
	faults     []error

	// guarded and logger are settled in wire and selected in seal, which the
	// driver runs in that order on one goroutine; selected is read by request
	// goroutines afterwards, and the order is what makes it safe to read
	// without a lock. logger is for the records this module writes with no
	// request behind them: a request's own records go through the logger its
	// context carries.
	selected tenant.Extractor
	guarded  []string
	logger   *slog.Logger
}

// The resolver delivers both route guards. The assertion is what keeps the
// descriptor's two Provisions and the methods one value has to carry together.
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

// open begins the registration window.
func (r *resolver) open() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openWindow = true
}

// Register implements tenant.Resolution.
//
// Registration is accepted from this module's Migrate callback until its Start
// callback, a window that contains the whole Init stage. A call outside that
// window is a mistake at the call site rather than a condition to report, and
// it panics, as the entry point's own registration surface does.
//
// A malformed registration and a duplicate name are recorded rather than
// raised: registrations arrive from other modules' callbacks while the window
// is open, where there is no caller to return an error to, and the window has
// to stay open for the rest of the stage. seal reports them once it closes.
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

// wire takes up the entry point and the backends and registers the layer on
// every endpoint this module guards.
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

// registerBuiltins puts the three extractors this module ships on the
// registration surface.
//
// They go through it rather than around it, so that a module registering its
// own is doing exactly what this module does and no more. They are registered
// here, in this module's Init callback, where every instance already exists and
// the window is still open; a host that configures one of them by name is
// configuring something this module put there, and a host that configures a
// name nobody registered is told so at seal.
func (r *resolver) registerBuiltins() {
	r.Register(HeaderExtractor, headerExtractor{name: r.settings.header})
	r.Register(SubdomainExtractor, subdomainExtractor{index: r.settings.subdomain})
	r.Register(PathExtractor, pathExtractor{index: r.settings.path})
}

// seal closes the window, reports what was wrong with it, and settles which
// extractor is in force.
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

// middlewareFor builds the layer registered on one endpoint.
//
// Provides carries the same token the module descriptor delivers the capability
// under. That is the only anchor another layer has for placing itself inside or
// outside tenant resolution, which makes it this contract surface on the chain.
//
// It sits in the identifying stage, alongside the authentication gateway. The
// After points at the authentication capability, a constraint within the stage,
// so the layer sits inside authentication: a request that authentication refused
// never reaches here, and neither does the roster lookup it would have driven.
// On an exempt route authentication passes the request through without settling
// an identity, and this layer still runs — which is what puts a tenant behind
// login and registration, where it is needed most.
func (r *resolver) middlewareFor() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*tenant.Resolution)(nil)},
		Stage:    speedhttp.StageIdentify,
		After:    []core.Token{(*authn.Gate)(nil)},
		Handler:  r.handle,
	}
}

// Guard implements speedhttp.RouteGuard: it wraps one route of a tenant-scoped
// group so that the tenant its path names is the tenant the request resolved to.
//
// The guard is delivered as a capability rather than registered as an endpoint
// layer because the routes that need it share an endpoint with platform routes
// that name a tenant as data and must not be held to this rule. The mounting
// module applies it to the routes of a ScopeTenants group only: pkg/authn/mount
// does so for the members, invitations and SSO groups, and the tenant
// administration modules do so for their own routes. Delivering it from this
// module keeps the roster where resolution already reads it, so a mounting
// module never takes the tenant directory into its own closure.
func (r *resolver) Guard(next gin.HandlerFunc) gin.HandlerFunc {
	return pathguard.New(r.roster).Guard(next)
}

// GuardTenantAdmin implements speedhttp.TenantAdminRouteGuard: it wraps one
// route of a tenant management surface with the identity rule and the refusal
// of the platform domain.
//
// The refusal is not part of the shared rule because the members, invitations
// and SSO groups manage the system tenant itself and are addressed by the
// platform domain legitimately. Only the tenant administration modules — the
// authz and settings tenant surfaces — act on a tenant, which the platform
// domain is not, so only they apply this capability.
func (r *resolver) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	return pathguard.NewTenantAdmin(r.roster).GuardTenantAdmin(next)
}

// handle is the layer. A request that resolves goes on to the layers inside
// with the tenant in its context; one that does not is answered by admit and
// the chain ends there.
func (r *resolver) handle(c *gin.Context) {
	if !r.admit(c) {
		return
	}
	c.Next()
}

// admit resolves one request and puts the tenant into its context, reporting
// whether the chain may go on. A false result means a response has been written
// through speedhttp.Fail, which has also ended the chain.
//
// The layer does not read the route: it answers which tenant a request belongs
// to, and a request that matched no route and one whose method the path does
// not allow are resolved like any other.
//
// The three ways to fail close with one status and are told apart in the log.
// "No such tenant" and "that tenant is disabled" are the same thing to a
// caller — this request belongs to no tenant being served — and answering them
// differently would let one tell a name that never existed from one that was
// switched off, which is a way to enumerate the roster.
//
// A panic inside this function is this module's own defect — an extractor
// registered through the seam runs here — and is recovered here, answered with
// a 500 and recorded under this module's name. Left to propagate it reaches the
// entry point's fallback, whose answer does not say which layer failed. The call
// to the next layer is deliberately outside this function: the handler's panic
// is not this layer's to catch, and catching it here would change what every
// route behind the layer does on one.
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
		// The roster is in memory and loaded at startup, so reaching this
		// means this module is not in a state it can answer from at all.
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

// lookUp resolves the name the selected extractor read to the tenant it names.
//
// The name is an alias and only an alias. The identifier is internal and is
// never a name a request surface may carry: a request that sends one is a
// request for a tenant no name reaches, and it is refused like any other
// unknown name rather than resolved through a second lookup.
func (r *resolver) lookUp(ctx context.Context, name string) (tenant.Tenant, bool, error) {
	return r.roster.ByAlias(ctx, name)
}

// refuse answers the one status this layer refuses with, and records why.
//
// The response carries the status and nothing else. It says the request belongs
// to no tenant being served; what that means for the API is the module that
// provides it, and the shape of the response is the entry point's.
//
// The reason goes to the log and not to the caller, which is what keeps a name
// that never existed from being told apart from one that was switched off.
//
// The record goes through the request's logger, not the one this module holds.
// It is about one request, and the request's logger carries what the layers
// outside bound — the trace, the subject the authentication layer settled —
// so an authenticated caller trying tenant names is tied to its attempts. The
// module's own logger carries none of that; it is for records with no request
// behind them.
func (r *resolver) refuse(c *gin.Context, why, name string) {
	attrs := []any{"path", c.Request.URL.Path, "reason", why}
	if name != "" {
		attrs = append(attrs, "name", name)
	}
	log.FromContext(c.Request.Context()).Info(diagnosticPrefix+ModuleName+": request refused", attrs...)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

// checkExtractorName refuses a name no module could select from configuration.
//
// A blank is any character Unicode counts as space, wherever it stands: a
// vertical tab or a no-break space is as invisible in a configuration file and
// a startup line as a space is, and a name carrying one reads back as a
// different name.
func checkExtractorName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: the name is empty", tenant.ErrInvalidExtractor)
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: %q carries a blank", tenant.ErrInvalidExtractor, name)
	}
	return nil
}

// isNilExtractor reports whether the implementation is absent, including the
// case of an interface holding a typed nil, which is not equal to nil but
// panics on the first call.
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

// registeredNames lists what was registered, for the message a wrong selection
// produces.
func registeredNames(m map[string]tenant.Extractor) string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
