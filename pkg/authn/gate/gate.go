// Package gate carries the authentication gateway: it registers a middleware
// layer on the guarded endpoints, decides who each request's caller is, and
// closes the decision into a pass or a status code, never logging a credential.
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

// ConfigNamespace is where this module's input items are mounted: this module's
// own name, beside each backend's section under the capability directory's heading.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the startup diagnostics,
// which have several writers; it names the capability directory, not the module.
const diagnosticPrefix = "authn: "

type gateway struct {
	settings settings
	// csrf is the cross-origin check a request whose credential the browser
	// attaches by itself, a cookie or a Basic header, is held to, built once from
	// the trusted origins.
	csrf *http.CrossOriginProtection

	// mu guards the registration window and what is registered through it; both
	// may be written from other modules' goroutines, so the order of writes is
	// not that of the drive.
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
	// assembly; it decides whether an empty mount setting takes the only endpoint.
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

var _ authn.Gate = (*gateway)(nil)

// init registers this module with the process registry: the import alone puts the
// gateway in the available set, so a dependency that reaches this package can get
// it assembled by a command taking in every available module.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers: it delivers the gateway in Init and
// is Exclusive with StateEnabled, so a second gateway fails startup rather than
// quietly losing its registrations.
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

		// The registration window opens in Migrate and seals in Start; a drive that
		// stops exactly at Migrate leaves it open, so a migration-only command
		// should not take this module in.
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

// productOf recovers this module's product; a nil instance or one of another type
// is tolerated rather than asserted, and every callback does nothing for it, so
// what such an instance holds is released by whoever supplied it.
func productOf(instance any) (*gateway, bool) {
	g, ok := instance.(*gateway)
	return g, ok && g != nil
}

// readerOf takes up the configuration reader; its absence is an error carried out
// as it stands, deliberately not read as "nothing was configured", which would lose
// the whole authentication layer in silence.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%sthe endpoints this gateway guards are read from "+
			"configuration, and no reader was handed to the registry before the assembly "+
			"began: %w", diagnosticPrefix, err)
	}
	return reader, nil
}

// prepare reads the configuration and always states that this module runs: a
// configuration it cannot run on is a startup failure naming the key, and leaving
// the module out of the command is the only way to have no gateway.
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
			// Only ErrUnknownEndpoint means the name is wrong; Router's contract
			// does not say nothing else can fail, and folding every error into
			// "correct the name" would point at a key already correct.
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

// wire runs in Init: it expands the guarded endpoints, builds the dispatch table,
// then registers the middleware, the redaction rules and the conclusion counter.
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

	// The layers are registered only now: each names the table's schemes, so it
	// has to exist first, and registering earlier takes the table while it is nil.
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

// diagnose writes what Init settled to the startup diagnostics: the guarded
// endpoints, the backends and what each takes, and the endpoints an impersonating
// identity can reach without a route registered for it.
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

// warnUnprefixedCookies writes one Warn per cookie a backend takes without the
// __Host- prefix: a sibling subdomain can plant it, and only a two-cookie request
// is refused. An http URL gets one Info instead, since WebKit refuses Secure there.
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
// registers the credential redaction rule over schemes, which can only run in Init;
// a logger that bound attributes earlier keeps them unmasked however late it writes.
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

// registrationAttrs renders one kind of the frozen tables for the diagnostics, one
// attribute per endpoint, listing its routes as method and path; it is written
// unconditionally, so a public route is said out loud even when none registered.
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

// checkMount holds each mount endpoint to its scope's rules: ordinary and system
// endpoints must be guarded and uncovered by a tenant layer, the tenant-administration
// endpoint covered by both; an empty default falls back only when routes are mounted.
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

// checkTenantScope holds the tenant-administration mount endpoint to the two layers
// that must cover it; outside them its routes would be served with no membership
// check.
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

// resolutionEndpoints reads the tenant resolution layer's endpoint list; false means
// it cannot be read at all, while a list nobody wrote reads as empty and readable,
// and the caller decides what an unreadable list means for its scope.
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
			// The layer is present and nobody wrote its list: it resolves on no
			// endpoint, which is a configuration read, not a shape that could not
			// be read.
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

// drain runs in Close: it waits for calls already inside a backend, bounded by the
// call budget. The waiter outlives it when the bound is reached, which is deliberate:
// a WaitGroup cannot be abandoned, and the goroutine ends with those calls.
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
