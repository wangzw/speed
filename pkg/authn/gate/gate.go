// Package gate carries the authentication gateway: the module that registers a
// middleware layer on the guarded listening endpoints, decides who each
// request's caller is, and closes the decision into a pass or a status code.
//
// It holds the dispatch table that says which credential-verifying backend
// answers for which carrier, the Authorization scheme or the cookie, and the
// per-endpoint tables of the routes registered Exempt, Optional and
// Impersonable. A host takes the gateway in by writing ModuleName into a
// command, together with at least one backend; the gateway itself implements no
// credential check and interprets no credential.
//
// What this package writes to a log is a category, an identifier and an outcome,
// and never a credential: not a request body, a form field, a cookie value, a
// token, a password, a one-time code, an assertion or an authorization code. A
// log line says that a cookie was refused and which backend refused it, not what
// the cookie held. The redaction rules the gateway registers with the logging
// capability (redact.go) are the second line behind that discipline and not a
// substitute for it: they mask the names and shapes this capability's values
// travel under, and a value that has neither is not caught.
//
// Nothing here is reached through the interface package. A registrant reaches
// the registration surface as authn.Gate, taken up from the registry, and a
// backend answers through authn.Verifier; both are in the parent package, which
// is what keeps a module that registers a route or delivers a backend from
// depending on this one.
package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose traffic is authenticated.
const ModuleName = "authn.gate"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, the way a registering module's namespace always is, so
// that each backend's section sits beside this one under the capability
// directory's heading and no module owns that heading itself.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers. It names the capability directory
// rather than the module: a reader scanning the diagnostics is looking for what
// authentication did, and the gateway is the only part of it that writes here.
const diagnosticPrefix = "authn: "

type gateway struct {
	settings settings
	// csrf is the cross-origin check a request whose credential the browser
	// attaches by itself, a cookie or a Basic header, is held to, built once from
	// the trusted origins.
	csrf *http.CrossOriginProtection

	// mu guards the registration window and what is registered through it.
	// Both are written from other modules' callbacks, which may register from a
	// goroutine of their own, so the order of the writes is not that of the
	// drive.
	mu       sync.Mutex
	phase    phase
	declared []registration

	// clock is the time the exit check holds an authentication time against. The
	// zero value reads the system clock; a test replaces it.
	clock clock.Source

	// router is taken up in Init and read again in Start, where the
	// registered endpoint names are checked against it.
	router speedhttp.Router
	// guarded is the set of endpoints this gateway guards, in ascending order,
	// expanded in Init from the configured list and the endpoints the entry
	// point declares. It is read-only afterwards.
	guarded []string
	// table is built in Init and read-only afterwards.
	table *dispatch
	// scope is the admission layer's statement of which endpoints it guards,
	// taken up in Init, and nil when the assembly has no admission layer.
	scope authn.ScopeGuard
	// mounted is whether a module that mounts the capability's routes is in the
	// assembly, found in Init by the Mounter it delivers. It decides whether
	// the endpoint those routes go on is judged when the configuration leaves it
	// empty.
	mounted bool
	// routes is frozen in Start, one table per endpoint that carries a
	// registration, and read-only afterwards. An endpoint with no registration
	// has no entry, and a lookup in the missing table finds nothing.
	routes map[string]map[route]access

	// logger is where the startup diagnostics go. Request-path records go
	// to the logger the entry point injected into the request context
	// instead, so that they carry that request's own correlation fields.
	logger *slog.Logger

	// slots is the in-flight gate: one token per call allowed to be inside
	// a backend at once.
	slots chan struct{}
	// callsMu guards stopped and the joining half of inFlight, so that a
	// call can never join after Stop has returned.
	callsMu  sync.Mutex
	stopped  bool
	inFlight sync.WaitGroup

	// counts are the buckets every request-time conclusion lands in, indexed
	// by bucket.
	counts [bucketCount]atomic.Int64
	// metrics is the callback that reports counts through the metric
	// capability, nil when the assembly carries none.
	metrics metric.Registration
}

// gateway must satisfy the capability it delivers.
var _ authn.Gate = (*gateway)(nil)

// init registers this module with the process registry.
//
// Registration travels with the import, which is what makes "does this binary
// have an authentication entry at all" a question the host answers by importing
// this subpackage. The interface package registers nothing, so the backends
// that implement its interfaces and the business code that reads an identity
// out of a context carry no module in with them.
//
// The cost is that an import is enough: a dependency that reaches this package
// for any reason puts the gateway in the available set, where a command taking
// in every available module will assemble it.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Requires holds one entry, an optional dependency on logging, so that the
// credential redaction rules a module owns are registered in its New, before
// any logger binds an attribute. Nothing else is required: the entry point, the
// backends, the scope guard, the route mounter and the metric capability are
// taken up in Init, where every instance already exists, so there is nothing for
// a construction-time requirement to order. Declaring one would also make the
// closure of a command that includes this module pull in whichever provider
// happens to be registered, which is the host's choice to write down, not this
// module's to force.
//
// The capability is delivered exclusively and the stance is StateEnabled. Two
// gateways on one endpoint would each register a layer and each decide the same
// request, so the pair is deliberate: resolution only stands a provider down
// when it claims exclusivity and states StateAuto, and a second gateway should
// fail the startup rather than quietly lose its registrations.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  []core.Provision{{Token: (*authn.Gate)(nil), Exclusive: true}},
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
			g, err := newGateway(set)
			if err != nil {
				return nil, err
			}
			if err := registerNewRules(reg); err != nil {
				return nil, err
			}
			return g, nil
		},

		// The window is opened in Migrate and sealed in Start, the two
		// stage slots the design's stage table gives it. The window that
		// leaves open contains the Init stage whole and reaches beyond it
		// at both ends, because a stage advances one module at a time:
		// this module's Migrate callback runs before the assembly enters
		// Init, and its Start callback runs once Init has ended. The rest
		// of the Migrate stage, and the Start callbacks ordered ahead of
		// this module, lie inside it too, and a registration from any of
		// them is accepted and takes effect.
		// Neither end could be this module's own Init callback — a module
		// that never declared a dependency on Gate may legally resolve it
		// and register from an Init that runs before this one. This
		// module migrates nothing and has no service of its own to start,
		// so the two slots have no other use.
		//
		// Whether either slot runs is the depth of the drive to decide,
		// and the two are tied by nothing. A drive shallower than Migrate
		// runs neither and reaches no Init: both sides of the window are
		// absent together and the result is consistent. A drive that stops
		// exactly at Migrate opens the window with nobody to seal it, and it
		// stays open for the rest of the process; that run has no Init, no
		// registration and no traffic. The way round it is for a command
		// whose purpose is to apply migrations not to take this module in.
		Migrate: func(_ context.Context, _ *core.Registry, instance any) error {
			if g, ok := productOf(instance); ok {
				g.open()
			}
			return nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			g, ok := productOf(instance)
			if !ok {
				return nil
			}
			return g.wire(reg)
		},

		Start: func(_ context.Context, reg *core.Registry, instance any) error {
			g, ok := productOf(instance)
			if !ok {
				return nil
			}
			return g.freeze(reg)
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if g, ok := productOf(instance); ok {
				g.stop()
			}
			return nil
		},

		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			g, ok := productOf(instance)
			if !ok {
				return nil
			}
			return errors.Join(g.drain(ctx), g.dropMeter())
		},
	}
}

// productOf recovers this module's product from what the registry hands back.
// A nil instance, or one of another type, is tolerated rather than asserted.
// The host may have supplied an instance of its own for this module, one that is
// not a gateway, and every callback does nothing for it: no layer is registered,
// no window opens or shuts, and what the instance holds is released by whoever
// supplied it. A startup that fails part-way also runs Stop and Close over every
// constructed instance whatever stage it reached.
func productOf(instance any) (*gateway, bool) {
	g, ok := instance.(*gateway)
	return g, ok && g != nil
}

// readerOf takes up the configuration reader.
//
// Its absence means the host never handed one to the registry before the drive
// began, which is core's own failure and is carried out as it stands: the fix is
// the host's, not a defect this module judged. It is deliberately not read as
// "nothing was configured" — that reading would let a host missing the reader
// lose the whole authentication layer in silence, which is the same hole as a
// zero verdict read as a pass.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%sthe endpoints this gateway guards are read from "+
			"configuration, and no reader was handed to the registry before the assembly "+
			"began: %w", diagnosticPrefix, err)
	}
	return reader, nil
}

// prepare reads the configuration and states that this module runs.
//
// There is no stance in which the gateway is switched off by what it reads: a
// command that names this module has promised it starts, so a configuration it
// cannot run on is a startup failure naming the key, and no key is required. The
// way to have no gateway is to leave the module out of the command.
func prepare(reader config.Reader) (core.Enablement, error) {
	if _, err := readSettings(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func newGateway(set settings) (*gateway, error) {
	csrf, err := mount.CrossOrigin(set.mount.PublicURL, set.trustedOrigins)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.%s and %s.%s do not give the cross-origin check an origin "+
			"set it can trust: %v", authn.ErrInvalidConfig, ConfigNamespace, mountPublicURLItem,
			ConfigNamespace, trustedOriginsItem, err)
	}
	return &gateway{
		settings: set,
		csrf:     csrf,
		slots:    make(chan struct{}, set.inFlight),
		logger:   log.Default(),
	}, nil
}

func (g *gateway) guards(endpoint string) bool {
	_, found := slices.BinarySearch(g.guarded, endpoint)
	return found
}

// guardedNames is the endpoints this gateway guards, in ascending order.
func (g *gateway) guardedNames() []string { return g.guarded }

// expandGuarded turns the configured list into the set of endpoints to guard.
// The reserved name stands for every endpoint the entry point declares; a list
// of names narrows the set to those, and each has to be one the entry point has.
func (g *gateway) expandGuarded(router speedhttp.Router) ([]string, error) {
	if g.settings.allGuarded() {
		all := router.Endpoints()
		if len(all) == 0 {
			return nil, fmt.Errorf("%w: %s.%s is %q, and the entry point declares no endpoint, "+
				"so there is nothing to guard", authn.ErrInvalidConfig, ConfigNamespace,
				endpointsItem, allEndpoints)
		}
		slices.Sort(all)
		return all, nil
	}
	names := slices.Clone(g.settings.endpoints)
	for _, name := range names {
		if _, err := router.Endpoint(name); err != nil {
			// Told apart the way the registration check tells them apart.
			// Router is an interface, and its contract says a name that was
			// never declared wraps ErrUnknownEndpoint — not that nothing else
			// can go wrong. Folding every failure into "correct the name"
			// would send a reader to a configuration key that is already
			// right.
			if errors.Is(err, speedhttp.ErrUnknownEndpoint) {
				return nil, fmt.Errorf("%w: %s.%s names the endpoint %q, which the entry point "+
					"does not have. Correct the name, or declare that endpoint in the entry "+
					"point's configuration", authn.ErrUnknownEndpoint, ConfigNamespace,
					endpointsItem, name)
			}
			return nil, fmt.Errorf("%staking up the endpoint %q named by %s.%s: %w",
				diagnosticPrefix, name, ConfigNamespace, endpointsItem, err)
		}
	}
	slices.Sort(names)
	return names, nil
}

// wire runs in Init: it expands the guarded endpoints, builds the dispatch
// table, registers the middleware on every guarded endpoint, registers the
// credential redaction rules that depend on the table, and registers the
// conclusion counter with the metric capability.
func (g *gateway) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return fmt.Errorf("%w: this command includes authentication but no HTTP entry "+
				"point, so there is nothing to authenticate. Write the entry "+
				"implementation subpackage's module name into this command",
				authn.ErrNoRouter)
		}
		return fmt.Errorf("%staking up the HTTP entry point to register on: %w",
			diagnosticPrefix, err)
	}
	g.router = router
	guarded, err := g.expandGuarded(router)
	if err != nil {
		return err
	}
	g.guarded = guarded

	table, err := buildDispatch(core.ResolveAll[authn.Verifier](reg))
	if err != nil {
		return err
	}
	g.table = table

	scope, err := core.Resolve[authn.ScopeGuard](reg)
	switch {
	case err == nil:
		g.scope = scope
	case errors.Is(err, core.ErrMissingProvider):
		// No admission layer: an impersonating identity is admitted to the
		// routes registered Impersonable alone.
	default:
		return fmt.Errorf("%staking up the scope guard to read which endpoints the "+
			"tenant admission layer guards: %w", diagnosticPrefix, err)
	}

	_, err = core.Resolve[mount.Mounter](reg)
	switch {
	case err == nil:
		g.mounted = true
	case errors.Is(err, core.ErrMissingProvider):
		// No module mounts routes in this assembly, so there is no endpoint
		// of theirs to judge unless the configuration names one.
	default:
		return fmt.Errorf("%staking up the route mounter to find out whether the capability's "+
			"own routes are mounted: %w", diagnosticPrefix, err)
	}

	// The layers are registered only now. Each is built from the dispatch
	// table — the challenge it answers a refusal with names the schemes in
	// it — so the table has to exist first. Registering earlier reads as
	// harmless and is not: it takes the table while it is still nil.
	for _, name := range g.guarded {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%staking up the endpoint %q to register on: %w",
				diagnosticPrefix, name, err)
		}
		endpoint.Use(g.middlewareFor(name))
	}

	if err := g.takeLogger(reg); err != nil {
		return err
	}
	if err := g.takeMeter(reg); err != nil {
		return err
	}
	g.diagnose()
	return nil
}

// diagnose writes what Init settled to the startup diagnostics: the endpoints
// that are guarded, the backends and what each takes, and the endpoints an
// impersonating identity can reach. Naming the guarded endpoints is what makes a
// missing guard a missing exemption, and the direction of that is refusal.
func (g *gateway) diagnose() {
	for _, b := range g.table.backends {
		g.logger.Info(diagnosticPrefix+"a credential-verifying backend taking part",
			slog.String("backend", b.name),
			slog.String("schemes", strings.Join(b.schemes, ", ")),
			slog.String("cookies", strings.Join(b.cookies, ", ")),
			slog.String("accounts", describeAccounts(b.accounts)),
			slog.Bool("impersonation", b.impersonation),
			slog.Bool("sso", b.sso))
	}
	g.logger.Info(diagnosticPrefix+"the endpoints this gateway guards",
		slog.String("endpoints", describeGuarded(g.guarded)),
		slog.String("configured", strings.Join(g.settings.endpoints, ", ")))
	g.logger.Info(diagnosticPrefix+"the endpoints an impersonating identity can reach without "+
		"a route registered for it",
		slog.String("endpoints", describeReach(g.reachable())))
	g.warnUnprefixedCookies()
}

const hostCookiePrefix = cookiename.HostPrefix

// warnUnprefixedCookies writes one Warn line for each cookie name a backend takes
// that does not carry the __Host- prefix. Such a cookie can be set by a page on a
// sibling subdomain, and the gateway has no defence against that beyond refusing
// a request that carries two cookies of the name: a request that carries only the
// planted one is indistinguishable from an honest one. The line is the
// relaxation said out loud, one per name, and it changes nothing the gateway does.
//
// On an http public URL the cookies carry no prefix and are not Secure by
// design, because WebKit refuses a prefixed cookie set over http even on a
// loopback host and WebKit on Linux refuses any Secure one there. Startup admits
// an http public URL only on a loopback host, whose traffic never leaves the
// machine and for which nothing but a process on the machine can set a cookie.
// One Info line says so instead.
func (g *gateway) warnUnprefixedCookies() {
	if cookiename.HTTP(g.settings.mount.PublicURL) {
		g.logger.Info(diagnosticPrefix+"the public URL is http, so the cookies on this origin carry no "+
			"__Host- or __Secure- prefix and are not Secure; it is admitted only on a loopback host, whose "+
			"traffic never leaves this machine, and only a process on this machine can set a cookie for it",
			slog.String("setting", ConfigNamespace+"."+mountPublicURLItem))
		return
	}
	for _, b := range g.table.backends {
		for _, name := range b.cookies {
			if strings.HasPrefix(name, hostCookiePrefix) {
				continue
			}
			g.logger.Warn(diagnosticPrefix+"a backend takes a cookie whose name does not begin with "+
				hostCookiePrefix+", so a page on a sibling subdomain can set a cookie of that name; "+
				"a request carrying two of them is refused, one carrying only the planted one is not",
				slog.String("backend", b.name), slog.String("cookie_name", name))
		}
	}
}

func (g *gateway) reachable() []string {
	var out []string
	if g.scope == nil {
		return out
	}
	for _, name := range g.guarded {
		if g.scope.Covers(name) {
			out = append(out, name)
		}
	}
	return out
}

func describeReach(endpoints []string) string {
	if len(endpoints) == 0 {
		return "none: an impersonating identity is admitted to routes registered " +
			"Impersonable alone"
	}
	return strings.Join(endpoints, ", ")
}

func describeAccounts(prefix string) string {
	if prefix == "" {
		return "the library namespace"
	}
	return prefix
}

// takeLogger takes up the logging capability for the startup diagnostics and
// registers the credential redaction rule that needs the dispatch table.
//
// Logging is optional for this module. The account and the backend are bound as
// correlation fields through log.WithAttrs, a package-level function that goes
// nowhere near the registry; only registering the rules needs the capability's
// instance, and registering them changes no authentication behaviour, so their
// absence changes none either. The key rules and the rule over value shapes are
// registered earlier, in New. The rule over a scheme's value has to wait until
// Init, because which schemes are in play is what the backends declared and the
// declarations are read there. That is weaker
// than the timing the log module offers a module for its own bindings: a logger
// that took attributes before the rule was registered keeps them unmasked
// however late the record is written.
func (g *gateway) takeLogger(reg *core.Registry) error {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			// No logging module in this assembly. There is nowhere to
			// register the rules, and the diagnostics go to the process
			// default logger.
			return nil
		}
		return fmt.Errorf("%staking up the logging capability to register the credential "+
			"value rule with: %w", diagnosticPrefix, err)
	}
	g.logger = delivered.Named(ModuleName)
	registerValueRule(delivered.Redaction(), g.table.schemes())
	return nil
}

func (g *gateway) freeze(reg *core.Registry) error {
	if err := g.seal(g.router); err != nil {
		return err
	}
	if err := g.checkMount(reg); err != nil {
		return err
	}
	g.logger.Info(diagnosticPrefix+"the exempt routes, frozen",
		registrationAttrs(g.routes, func(a access) bool { return a.exempt }, "exemptions",
			"none: every route on every guarded endpoint requires a credential")...)
	g.logger.Info(diagnosticPrefix+"the routes that accept a request with no credential, frozen",
		registrationAttrs(g.routes, func(a access) bool { return a.optional }, "optional_routes",
			"none")...)
	g.logger.Info(diagnosticPrefix+"the routes that admit an impersonating identity, frozen",
		registrationAttrs(g.routes, func(a access) bool { return a.impersonable },
			"impersonable_routes", "none")...)
	return nil
}

// registrationAttrs renders one kind of the frozen tables for the diagnostics,
// one attribute per endpoint, listing its routes as method and path. It is
// written unconditionally on the success path: a route that is public is worth
// saying out loud once at startup, whether or not anyone registered one.
func registrationAttrs(routes map[string]map[route]access, has func(access) bool, what, none string) []any {
	var attrs []any
	for _, endpoint := range sortedKeys(routes) {
		if listed := routesOf(routes[endpoint], has); len(listed) > 0 {
			attrs = append(attrs, slog.String(endpoint, strings.Join(listed, ", ")))
		}
	}
	if len(attrs) == 0 {
		return []any{slog.String(what, none)}
	}
	return attrs
}

// The configuration keys of the endpoints the capability's scopes mount on,
// relative to the gateway's namespace.
const (
	defaultMountItem = mountEndpointsItem + ".default"
	tenantsMountItem = mountEndpointsItem + ".tenants"
	systemMountItem  = mountEndpointsItem + ".system"
)

// checkMount holds every endpoint the capability's routes are mounted on to the
// rules this module can judge, in the direction each scope's policy domain
// requires.
//
// The ordinary and platform-management routes are decided outside the tenant's
// domain: the gateway has to guard their endpoint, and neither tenant layer may
// cover it. The admission layer answers an impersonating request with no tenant
// context 404, which would leave an impersonating caller unable even to end the
// impersonation, and the resolution layer answers a global account's request,
// which carries no tenant name, the same way. The tenant-administration routes
// are the reverse: they carry a tenant in their path and are decided in that
// tenant's policy domain, so their endpoint has to be one both tenant layers
// cover, or the route would serve a non-member with no membership check.
//
// The endpoints judged are the ones the routes actually go on. A written value
// is judged whether or not any module mounts routes. The default and system
// endpoints fall back to the entry point's only endpoint when unwritten, and
// only when a module that mounts routes is in the assembly (g.mounted): a
// command that mounts nothing has no endpoint of that kind, and judging the only
// endpoint it has would refuse one whose tenant resolution is perfectly in
// order. The tenant endpoint has no fallback, and its check runs only when the
// key is written: an unwritten key leaves the scope unmountable, and the module
// that mounts routes refuses a tenant-scoped registration on it. The module that
// mounts routes settles the empty default in its own Init and reports an entry
// point it cannot settle on (ErrRoutesUnconfigured) there, before this runs; the
// same rule is read again here, so an implementation that did not check still
// gets the same answer. An only endpoint is always one this gateway guards, the
// guarded set being a non-empty subset of the declared endpoints, so the guard
// check cannot fail for the defaulted case and is not special-cased.
//
// The tenant resolution layer's endpoint list is another module's, read when its
// shape can be read at all: for the ordinary surfaces a shape that cannot be
// decoded is a line of warning and not a failed startup, since the routes are
// safe on an endpoint nobody showed to be resolved on. For the tenant scope the
// requirement is coverage, so a list that cannot be read fails the startup: the
// routes would otherwise rest on an assumption this module cannot verify.
func (g *gateway) checkMount(reg *core.Registry) error {
	configured := g.settings.mount.Endpoints
	if configured.Default == "" && configured.Tenants == "" && configured.System == "" && !g.mounted {
		return nil
	}
	declared := g.router.Endpoints()
	defaultEndpoint, err := mount.Endpoint(configured.Default, declared, ConfigNamespace+"."+defaultMountItem)
	if err != nil {
		return err
	}
	defaultWhere := fmt.Sprintf("%s.%s is %q", ConfigNamespace, defaultMountItem, defaultEndpoint)
	if configured.Default == "" {
		defaultWhere = fmt.Sprintf("%s.%s is empty, which takes the entry point's only endpoint, %q",
			ConfigNamespace, defaultMountItem, defaultEndpoint)
	}
	systemEndpoint, systemWhere := configured.System, ""
	if systemEndpoint == "" {
		systemEndpoint = defaultEndpoint
		systemWhere = fmt.Sprintf("%s.%s is empty, which takes the default endpoint, %q",
			ConfigNamespace, systemMountItem, systemEndpoint)
	} else {
		systemWhere = fmt.Sprintf("%s.%s is %q", ConfigNamespace, systemMountItem, systemEndpoint)
	}
	for _, surface := range []struct {
		item     string
		where    string
		endpoint string
	}{
		{defaultMountItem, defaultWhere, defaultEndpoint},
		{systemMountItem, systemWhere, systemEndpoint},
	} {
		if err := g.checkSurface(reg, surface.item, surface.where, surface.endpoint); err != nil {
			return err
		}
	}
	if configured.Tenants != "" {
		where := fmt.Sprintf("%s.%s is %q", ConfigNamespace, tenantsMountItem, configured.Tenants)
		return g.checkTenantScope(reg, where, configured.Tenants)
	}
	return nil
}

// checkSurface holds one ordinary or platform-management mount endpoint to the
// gateway and to the two layers that must not cover it.
func (g *gateway) checkSurface(reg *core.Registry, item, where, endpoint string) error {
	if err := g.checkMountEndpoint(item, endpoint); err != nil {
		return err
	}
	if g.scope != nil && g.scope.Covers(endpoint) {
		return fmt.Errorf("%w: %s, an endpoint the tenant admission layer guards. That "+
			"layer answers an impersonating request without a tenant context with 404, so an "+
			"impersonating caller could not even end the impersonation. Mount the routes on an "+
			"endpoint the admission layer does not guard", authn.ErrInvalidConfig, where)
	}
	listed, readable := g.resolutionEndpoints(reg)
	if !readable {
		return nil
	}
	if slices.Contains(listed, endpoint) {
		return fmt.Errorf("%w: %s, which %s.%s also lists. A global account's request "+
			"carries no tenant name, and the tenant resolution layer answers it with 404, so "+
			"the authentication routes would be unreachable to it. Mount the routes on an "+
			"endpoint the resolution layer does not resolve", authn.ErrInvalidConfig,
			where, resolutionNamespace, resolutionEndpointsItem)
	}
	return nil
}

// checkTenantScope holds the tenant-administration scope's mount endpoint to the
// two layers that must cover it: its routes are decided in the tenant's policy
// domain, and an endpoint outside those layers would serve them with no tenant
// and no membership check.
func (g *gateway) checkTenantScope(reg *core.Registry, where, endpoint string) error {
	if err := g.checkMountEndpoint(tenantsMountItem, endpoint); err != nil {
		return err
	}
	if g.scope == nil || !g.scope.Covers(endpoint) {
		return fmt.Errorf("%w: %s, the tenant-administration scope's mount endpoint, which "+
			"the tenant admission layer does not cover. Those routes are decided in the "+
			"tenant's policy domain, and an endpoint the layer does not guard would serve them "+
			"with no membership check. Write %s.%s as an endpoint the layer covers, or mount "+
			"them on one", authn.ErrInvalidConfig, where, ConfigNamespace, tenantsMountItem)
	}
	listed, readable := g.resolutionEndpoints(reg)
	if !readable {
		return fmt.Errorf("%w: %s, the tenant-administration scope's mount endpoint, which "+
			"cannot be shown to be one %s.%s resolves a tenant on: its list could not be read "+
			"at startup. Those routes carry a tenant in their path, so the resolution layer "+
			"has to run there. Write %s.%s as an endpoint that layer resolves on", authn.ErrInvalidConfig,
			where, resolutionNamespace, resolutionEndpointsItem, ConfigNamespace, tenantsMountItem)
	}
	if !slices.Contains(listed, endpoint) {
		return fmt.Errorf("%w: %s, the tenant-administration scope's mount endpoint, which "+
			"%s.%s does not list. Those routes carry a tenant in their path, so the resolution "+
			"layer has to resolve a tenant there. Write %s.%s as an endpoint that layer resolves "+
			"on, or mount them on one", authn.ErrInvalidConfig, where, resolutionNamespace,
			resolutionEndpointsItem, ConfigNamespace, tenantsMountItem)
	}
	return nil
}

// checkMountEndpoint holds one mount endpoint to what every scope requires: the
// entry point declares it and the gateway guards it, or the routes would be
// served by a layer nobody asked to authenticate them.
func (g *gateway) checkMountEndpoint(item, endpoint string) error {
	if _, err := g.router.Endpoint(endpoint); err != nil {
		if errors.Is(err, speedhttp.ErrUnknownEndpoint) {
			return fmt.Errorf("%w: %s.%s names the endpoint %q, which the entry point does not "+
				"have", authn.ErrUnknownEndpoint, ConfigNamespace, item, endpoint)
		}
		return fmt.Errorf("%schecking the endpoint %q the routes are mounted on: %w", diagnosticPrefix,
			endpoint, err)
	}
	if !g.guards(endpoint) {
		return fmt.Errorf("%w: the authentication routes are mounted on the endpoint %q, which "+
			"this gateway does not guard (it guards %s). Write that endpoint into %s.%s, or "+
			"mount the routes on another", authn.ErrRoutesUnguarded, endpoint,
			describeGuarded(g.guarded), ConfigNamespace, endpointsItem)
	}
	return nil
}

// resolutionNamespace and resolutionEndpointsItem are where the tenant
// resolution layer keeps the endpoints it resolves a tenant on. They are read,
// not owned: the list is that module's, and this module reads it once, in Start.
const (
	resolutionNamespace     = "tenant.resolve"
	resolutionEndpointsItem = "endpoints"
)

// resolutionEndpoints reads the tenant resolution layer's endpoint list. The
// second result is false when the list cannot be read at all: no reader was
// handed to the registry, or the shape is not one this module can decode. A list
// nobody wrote reads as empty and is readable, since nothing is resolved on any
// endpoint. A decoding failure is a line of warning; whether that is enough is
// the caller's to decide, because the ordinary surfaces and the tenant scope
// make opposite requirements of the same list.
func (g *gateway) resolutionEndpoints(reg *core.Registry) ([]string, bool) {
	if reg == nil {
		return nil, false
	}
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		g.logger.Warn(diagnosticPrefix+"could not take up the configuration reader to check the "+
			"mount endpoint against the tenant resolution layer's endpoints",
			slog.String("cause", err.Error()))
		return nil, false
	}
	var shape struct {
		Endpoints []string `config:"endpoints"`
	}
	if err := reader.Decode(resolutionNamespace, &shape); err != nil {
		if errors.Is(err, config.ErrMissingRequired) {
			// The layer is in the assembly and nobody wrote its endpoint
			// list: it resolves a tenant on no endpoint, so no mount
			// endpoint is one of them. That is a configuration read, not
			// a shape this module could not read.
			return nil, true
		}
		g.logger.Warn(diagnosticPrefix+"could not read the tenant resolution layer's endpoint "+
			"list, so the mount endpoints are not checked against it",
			slog.String("key", resolutionNamespace+"."+resolutionEndpointsItem),
			slog.String("cause", err.Error()))
		return nil, false
	}
	return shape.Endpoints, true
}

func (g *gateway) stop() {
	g.callsMu.Lock()
	g.stopped = true
	g.callsMu.Unlock()
}

// drain runs in Close: it waits for the calls already inside a backend, bounded
// by the call budget, which is the longest any one of them should still take.
//
// The waiter below outlives this function when the bound is reached, and that is
// the best available answer rather than an oversight: a WaitGroup cannot be
// abandoned, and the only alternative is to keep Close waiting on a backend that
// has stopped answering, which would hold the whole shutdown open. The goroutine
// ends when those calls end, and Close runs once per process, so nothing
// accumulates.
func (g *gateway) drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.inFlight.Wait()
		close(done)
	}()

	timer := time.NewTimer(g.settings.budget)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: Close waited the call budget of %s (%s.%s) and gave up the wait",
			authn.ErrDrainTimeout, g.settings.budget, ConfigNamespace, budgetItem)
	case <-ctx.Done():
		return fmt.Errorf("%swaiting for in-flight backend calls: %w", diagnosticPrefix, ctx.Err())
	}
}

func (g *gateway) tally() map[string]int64 {
	out := make(map[string]int64, len(g.counts))
	for i := range g.counts {
		out[bucketName(i)] = g.counts[i].Load()
	}
	return out
}
