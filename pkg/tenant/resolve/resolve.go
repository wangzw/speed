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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
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

// tenantField is the name the resolved identifier is bound to on the logger.
//
// The field name is this module's to choose — the logging module knows no
// domain fields — and it is a contract once a dashboard reads it, so it is
// fixed here rather than left to each call site.
const tenantField = "tenant_id"

// middlewareOrder places this layer where the constraints leave a choice.
//
// It is a preference, not a constraint. Where this layer has to sit is decided
// by pointing at the authentication capability, which is what its own Provides
// declares; this value only decides where it lands when authentication is not
// in the assembly at all.
const middlewareOrder = 90

// The module registers itself. Importing this package is what puts tenant
// resolution in a binary's available set; a host that never imports it carries
// no resolution layer, whatever else of this capability it takes in.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The capability is delivered exclusively. Two resolving layers on one endpoint
// would each decide the same request, and which of them wrote the identifier
// downstream reads is not something either could answer — so the pair is
// deliberately unrepresentable, and a second one fails the startup instead.
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
// and not the roster's is left with no provider for the capability this module
// requires — which ends the run at dependency resolution, before anything is
// constructed. The two module names go into the command together.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*tenant.Catalog)(nil)}},
		Provides:  []core.Provision{{Token: (*tenant.Resolution)(nil), Exclusive: true}},
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
// the host never supplied one, which is the host's omission and is carried out
// as it stands rather than read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	return core.Resolve[config.Reader](reg)
}

// productOf narrows an instance to this module's product.
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
// After points at the authentication capability, so the layer sits inside
// authentication: a request that authentication refused never reaches here, and
// neither does the roster lookup it would have driven. On an exempt route
// authentication passes the request through without settling an identity, and
// this layer still runs — which is what puts a tenant behind login and
// registration, where it is needed most.
func (r *resolver) middlewareFor() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*tenant.Resolution)(nil)},
		After:    []core.Token{(*authn.Gate)(nil)},
		Order:    middlewareOrder,
		Wrap: func(next nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
				resolved := r.admit(w, req)
				if resolved == nil {
					return
				}
				next.ServeHTTP(w, resolved)
			})
		},
	}
}

// admit resolves one request, returning it with the tenant in its context, or
// nil once a status has been written.
//
// The three ways to fail close with one status and are told apart in the log.
// "No such tenant" and "that tenant is disabled" are the same thing to a
// caller — this request belongs to no tenant being served — and answering them
// differently would let one tell a name that never existed from one that was
// switched off, which is a way to enumerate the roster.
func (r *resolver) admit(w nethttp.ResponseWriter, req *nethttp.Request) *nethttp.Request {
	name, ok := r.selected.Extract(req)
	if !ok {
		r.refuse(w, req, "the request carries no tenant name", "")
		return nil
	}
	found, present, err := r.roster.ByAlias(req.Context(), name)
	if err != nil {
		// The roster is in memory and loaded at startup, so reaching this
		// means this module is not in a state it can answer from at all.
		log.FromContext(req.Context()).Error(diagnosticPrefix+ModuleName+
			": the roster could not be read", "error", err)
		w.WriteHeader(nethttp.StatusInternalServerError)
		return nil
	}
	if !present {
		r.refuse(w, req, "the name is not on the roster", name)
		return nil
	}
	if found.Status != tenant.StatusActive {
		r.refuse(w, req, "the tenant is not being served", name)
		return nil
	}
	ctx := tenantctx.WithID(req.Context(), string(found.ID))
	ctx = log.WithAttrs(ctx, slog.String(tenantField, string(found.ID)))
	return req.WithContext(ctx)
}

// refuse writes the one status this layer answers with, and records why.
//
// No body is written. The status says the request belongs to no tenant being
// served; what that means for the API is the module that provides it, and this
// layer does not know which one that is.
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
func (r *resolver) refuse(w nethttp.ResponseWriter, req *nethttp.Request, why, name string) {
	attrs := []any{"path", req.URL.Path, "reason", why}
	if name != "" {
		attrs = append(attrs, "name", name)
	}
	log.FromContext(req.Context()).Info(diagnosticPrefix+ModuleName+": request refused", attrs...)
	w.WriteHeader(nethttp.StatusNotFound)
}

// checkExtractorName refuses a name no module could select from configuration.
func checkExtractorName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: the name is empty", tenant.ErrInvalidExtractor)
	}
	for _, c := range name {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return fmt.Errorf("%w: %q carries a blank", tenant.ErrInvalidExtractor, name)
		}
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
