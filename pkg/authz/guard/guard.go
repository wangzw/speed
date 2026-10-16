// Package guard is the authorization enforcement module. It delivers four
// faces: the request-path middleware on the authorizing stage, the
// route-declaration registry behind authz.Gate, the handler-internal Guard API,
// and the authentication capability's decision seam authn.Decider — so a
// module asking whether an actor may do a thing is answered from this module's
// policy, and a route is enforced on the chain before its handler runs.
//
// The middleware and the decider share one closed translation from a permission
// to the (object, action) pair the engine matches on, one declaration table, and
// one read of the tenant: a permission cannot mean one thing on one path and
// another on the other.
package guard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose requests are authorized.
const ModuleName = "authz.guard"

// The names of the metric: the counter, the meter it is created on, and the
// attribute that tells the outcomes apart. The counter is authz.guard.decisions
// and the attribute outcome; a dashboard written against them stops matching
// when one changes.
const (
	decisionsInstrument = ModuleName + ".decisions"
	outcomeAttribute    = "outcome"
	meterName           = "authz"
)

// The module registers itself. Importing this package is what puts the
// enforcement middleware and the decider in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers the route-declaration anchor, the handler-internal Guard API and
// the decision seam, each exclusively: two declarations of what a route needs,
// two handler APIs, and two implementations of the seam would each leave it
// unsaid which of them answered. A second provider of any of them fails the
// startup.
//
// It requires the engine and the catalog, both of which the policy module
// delivers exclusively, so a command that asks this module to decide names the
// policy module alongside it: an engine with no policy to match against is not a
// decider, and the catalog is what the route-declaration surface checks a
// declared permission against. The HTTP entry point is taken up in Init rather
// than declared as a requirement, exactly as the admission layer takes it: a
// command that guards routes and has no entry point has nothing to guard, which
// is a failure to report at wiring time and not a dependency for resolution. The
// logger and the meter provider are optional: the decision reads neither, and
// their absence is a legal configuration.
//
// Prepare states StateEnabled and never StateAuto: the module taken in at run
// level must start, so whether it runs is a decision of the command tree.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authz.Enforcer)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*observability.MeterProvider)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authz.Gate)(nil), Exclusive: true},
			{Token: (*Guard)(nil), Exclusive: true},
			{Token: (*authn.Decider)(nil), Exclusive: true},
		},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			enforcer, err := core.Resolve[authz.Enforcer](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the enforcement engine: %w", ModuleName, err)
			}
			catalog, err := core.Resolve[authz.Catalog](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the permission catalog: %w", ModuleName, err)
			}
			return &product{
				enforcer: enforcer,
				catalog:  catalog,
				gate:     newGate(catalog),
				cfg:      cfg,
				logger:   log.Default(),
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			if err := p.gate.verify(); err != nil {
				// A failed Start must not leave the resolved translation
				// installed: a later assembly sharing the process would read a set
				// this one never accepted. The set belongs to the policy module,
				// which resolved it; this is the beat at which an assembly that
				// did not come up gives it back.
				translation.Reset()
				return err
			}
			return p.registerMetrics()
		},

		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.dropMeter()
		},
	}
}

// Guard is the handler-internal decision API: a handler asks this module one
// question about one permission after the middleware has let the request
// through. It only tightens what the route declaration already decided and
// never widens it — RequirePermission that finds the engine saying no writes the
// same 403 the middleware writes, and it does not read the declaration table.
//
// It lives here and not in the root package because its signature takes a
// *gin.Context, which the root package cannot carry without importing gin. Its
// writing method is named RequirePermission rather than Require because one type
// cannot carry two methods of one name: the same product delivers the
// route-declaration surface authz.Gate, whose Require takes a method and a path,
// and a second method named Require would collide with it and leave the product
// unable to deliver both capabilities.
type Guard interface {
	// Allowed answers whether the identity on the request may do perm in the
	// request's own domain — the tenant it resolved to, or the platform domain
	// when it resolved none. A false with no error is a denial; a non-nil error
	// is an outage the caller reads as a 503. A request with no identity, or a
	// permission this module cannot express, is refused.
	Allowed(c *gin.Context, perm authz.Permission) (bool, error)
	// RequirePermission is Allowed with the refusal written: it answers whether
	// the request may go on, writing a 403 for a denial and a 503 for an outage,
	// and returning false in both cases.
	RequirePermission(c *gin.Context, perm authz.Permission) bool
	// RequirePermissionIn is RequirePermission with the policy domain named
	// explicitly rather than read from the request: it answers whether the
	// identity on the request may do perm in domain, writing the same refusals.
	//
	// It is for a caller whose decision must not be steered by whatever tenant
	// the request happened to resolve to — a management surface that names a
	// tenant in its path as data and decides in a fixed domain of its own, so
	// that a caller authorized in one tenant cannot reach another's data by
	// naming it. Reading the domain from the context would let the two drift.
	RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool
}

// product is this module's product. It implements authn.Decider, the
// handler-internal Guard, and holds the declaration table the middleware reads.
type product struct {
	enforcer authz.Enforcer
	catalog  authz.Catalog
	gate     *gate
	cfg      resolved
	logger   *slog.Logger

	// meter is settled in wire, and counts in registerMetrics. Both run before
	// any request is served; the request path writes counts and reads nothing
	// else here.
	meter   observability.MeterProvider
	counts  [outcomeCount]atomic.Int64
	metrics metric.Registration
}

var (
	_ authn.Decider = (*product)(nil)
	_ Guard         = (*product)(nil)
	_ authz.Gate    = (*product)(nil)
)

// Require implements authz.Gate. The declaration table lives on the product's
// gate; the product carries the capability so a module's Init can take the
// anchor up from the registry the descriptor declares it under.
func (p *product) Require(method, path string, perm authz.Permission) {
	p.gate.Require(method, path, perm)
}

// Open implements authz.Gate.
func (p *product) Open(method, path string) { p.gate.Open(method, path) }

// Covered implements authz.Gate.
func (p *product) Covered(method, path string) bool { return p.gate.Covered(method, path) }

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for the module, and every callback leaves it
// alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok
}

// optional takes up a capability that may be absent. Absence is the legal
// configuration and leaves the zero value; any other failure is carried out and
// stops the startup, where leaving the capability out would change what the
// layer enforces without a word.
func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		var zero T
		return zero, false, nil
	}
	var zero T
	return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
}

// wire takes up what the middleware reads, registers it on every guarded
// endpoint and writes the startup diagnostics.
//
// The entry point is required: a command that guards routes and has no entry
// point has nothing to guard, and the failure names the module so the fix —
// writing the entry point module's name into this command — is visible. Every
// endpoint in the configured list is looked up on the entry point; a name the
// entry point does not declare is ErrUnknownEndpoint and stops the startup,
// because a guard on an endpoint nobody serves guards nothing.
//
// The process's translation set is resolved here, and the policy module resolves
// it too. Resolving is a pure function of the registry and an idempotent write,
// so the two agree by construction and the second one to run costs a map copy.
// Both do it because either may be the only one present: this module reads the
// set on every decision, and a command that writes policy data without guarding
// anything — a bootstrap that seeds roles — takes the policy module in and leaves
// this one out, with no HTTP entry point to satisfy.
func (p *product) wire(reg *core.Registry) error {
	if err := translation.Resolve(reg); err != nil {
		return err
	}
	if delivered, found, err := optional[log.Logger](reg, "the logger"); err != nil {
		return err
	} else if found {
		p.logger = delivered.Named(ModuleName)
	}
	if meter, found, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if found {
		p.meter = meter
	}

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s: %w", ModuleName, authz.ErrNoRouter)
	}
	for _, name := range p.cfg.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: %w: %q: %w", ModuleName, authz.ErrUnknownEndpoint, name, err)
		}
		endpoint.Use(p.middlewareFor(name))
	}

	p.logger.Info(ModuleName+": authorization in force",
		"guarded", p.cfg.endpoints,
		"undeclared", undeclaredName(p.cfg.undeclared))
	if p.cfg.undeclared == undeclaredOpen {
		p.logger.Warn(ModuleName + ": " + undeclaredKey + " is open: a route on a guarded endpoint " +
			"that carries no declaration is let through. Declare the route's permission, or register " +
			"it open, and change this back to " + closedValue)
	}
	return nil
}

// undeclaredName renders the disposition for the startup diagnostics. The zero
// value reads closed, the same way the parse reads an unset item.
func undeclaredName(m undeclaredMode) string {
	if m == undeclaredOpen {
		return openValue
	}
	return closedValue
}

// middlewareFor builds the layer registered on one endpoint.
//
// Provides carries the token the descriptor delivers the declaration anchor
// under — the chain anchor lives in the root package, and another module sort
// against it. The layer declares the authorizing stage and, within it, sits
// inside the tenant admission anchor: identity and tenant are settled before
// this layer reads them, and the anchor is what places it there when the
// admission layer is present.
func (p *product) middlewareFor(endpoint string) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*authz.Gate)(nil)},
		Stage:    speedhttp.StageAuthorize,
		After:    []core.Token{(*tenant.Admission)(nil)},
		Handler: func(c *gin.Context) {
			p.handle(c, endpoint)
		},
	}
}

// handle is the request-path decision. It reads the declaration table top down
// and stops at the first row it hits.
//
// A request that matched no route is handed on — the entry point answers its own
// 404, and this layer has no route to declare anything about. A route declared
// open is let through. A route declared to need a permission goes to enforce. A
// route with no declaration at all is refused under the closed default and let
// through under the open one.
func (p *product) handle(c *gin.Context, realm string) {
	path := c.FullPath()
	if path == "" {
		c.Next()
		return
	}
	method := c.Request.Method
	if p.gate.isOpen(method, path) {
		p.conclude(c, decision{outcome: allowed}, realm)
		c.Next()
		return
	}
	perm, required := p.gate.requirement(method, path)
	if !required {
		if p.cfg.undeclared == undeclaredOpen {
			p.conclude(c, decision{outcome: allowed}, realm)
			c.Next()
			return
		}
		p.conclude(c, decision{outcome: unguarded}, realm)
		return
	}
	p.enforce(c, perm, realm)
}

// enforce decides a route that declared a required permission.
//
// With no identity on the request, the access level the gateway recorded tells
// the two cases apart: a route that is open or optional lets the anonymous
// request through, and one that requires identity is answered 401. With an
// identity, the query names the subject's accountability key and the tenant the
// request resolved to — the identifier, not the alias — or the platform domain
// when no tenant was resolved. The engine's answer is read as it stands: an
// error is an outage (503), a false is a denial (403), a true passes.
func (p *product) enforce(c *gin.Context, perm authz.Permission, realm string) {
	ctx := c.Request.Context()
	id, hasIdentity := authn.FromContext(ctx)
	if !hasIdentity {
		switch authn.AccessFromContext(ctx) {
		case authn.AccessOpen, authn.AccessOptional:
			p.conclude(c, decision{outcome: allowed}, realm)
			c.Next()
		default:
			p.conclude(c, decision{outcome: noIdentity, permission: perm}, realm)
		}
		return
	}
	object, action, ok := translation.Lookup(perm)
	if !ok {
		// A permission the translation does not hold is one this module cannot
		// express; the safe answer is a denial, the same as the decider gives an
		// unknown action.
		p.conclude(c, decision{outcome: denied, permission: perm}, realm)
		return
	}
	allow, err := p.enforcer.Allowed(ctx, authz.Query{
		Subject: id.Key(),
		Domain:  domainOf(c),
		Object:  object,
		Action:  action,
	})
	switch {
	case err != nil:
		p.conclude(c, decision{outcome: outage, cause: err, permission: perm}, realm)
	case !allow:
		p.conclude(c, decision{outcome: denied, permission: perm}, realm)
	default:
		p.conclude(c, decision{outcome: allowed}, realm)
		c.Next()
	}
}

// conclude counts a decision, records a refusal and writes its answer.
//
// Every request that reached a decision is counted once, under the outcome it
// got. A refusal leaves one line through the logger the request's context
// carries, so it has the fields the layers outside bound on it, and adds the
// outcome alone: a policy denial is Info, while the three refusals that mean
// something is wrong rather than someone is refused — a route nobody declared,
// a request with no identity on a route that requires one, and a decision that
// could not be made — are written at Error. The answer carries the status and
// nothing that tells the refusals apart from the reason; the permission a route
// asked for never reaches the response. The status each writer uses is
// outcome.status(), so the code this layer answers with is the one the closed
// set states.
func (p *product) conclude(c *gin.Context, d decision, realm string) {
	p.counts[d.outcome].Add(1)
	if d.outcome.passes() {
		return
	}
	logger := log.FromContext(c.Request.Context())
	switch d.outcome {
	case denied:
		logger.Info(ModuleName+": request refused", outcomeAttribute, d.outcome.String())
		forbidden(c, d.outcome, d.permission)
	case unguarded:
		logger.Error(ModuleName+": a route on a guarded endpoint carries no declaration and "+
			"undeclared is closed: declare the route's permission, register it open, or set "+
			undeclaredKey+" to "+openValue, outcomeAttribute, d.outcome.String(),
			"error", authz.ErrUndeclaredRoute)
		forbidden(c, d.outcome, d.permission)
	case noIdentity:
		logger.Error(ModuleName+": a request with no identity reached a route that needs one. "+
			"The gateway is not in front of this endpoint or does not guard it",
			outcomeAttribute, d.outcome.String())
		noIdentity401(c, d.outcome, realm)
	case outage:
		// unavailable writes the outage line itself, so both paths that reach it
		// leave the cause in the log.
		unavailable(c, d.outcome, d.cause)
	}
}

// domainOf reads the policy domain a handler-internal decision runs in when the
// domain is not named: the tenant the request resolved to, or the platform
// domain when it resolved none. It is the one read of the tenant the middleware
// and the handler API share.
func domainOf(c *gin.Context) authz.Domain {
	if tenantID, ok := tenant.FromContext(c.Request.Context()); ok {
		return authz.Domain(tenantID)
	}
	return authz.PlatformDomain
}

// Allowed implements Guard. It reads the identity and the tenant off the request
// and puts one query to the engine. A request with no identity, and a permission
// this module cannot express, are both refused without an error; an engine error
// is carried out as it stands.
func (p *product) Allowed(c *gin.Context, perm authz.Permission) (bool, error) {
	return p.allowedIn(c, perm, domainOf(c))
}

// RequirePermission implements Guard: Allowed with the refusal written. It
// writes a 503 for an outage and a 403 for a denial, and answers false in both
// cases. It only tightens what the middleware already allowed; it does not read
// the declaration table. Both refusals go through the same writers the
// middleware uses, so an outage reached this way leaves its cause in the log as
// the middleware's does.
func (p *product) RequirePermission(c *gin.Context, perm authz.Permission) bool {
	return p.requireIn(c, perm, domainOf(c))
}

// RequirePermissionIn implements Guard: RequirePermission with the policy domain
// named explicitly. It decides in domain and never reads the request's tenant,
// so a caller that asks in a fixed domain of its own cannot be steered by
// whatever tenant the request resolved to.
func (p *product) RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	return p.requireIn(c, perm, domain)
}

// allowedIn answers whether the identity on the request may do perm in domain,
// without writing anything. A request with no identity, and a permission this
// module cannot express, are both refused without an error; an engine error is
// carried out as it stands.
func (p *product) allowedIn(c *gin.Context, perm authz.Permission, domain authz.Domain) (bool, error) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		return false, nil
	}
	object, action, ok := translation.Lookup(perm)
	if !ok {
		return false, nil
	}
	return p.enforcer.Allowed(ctx, authz.Query{
		Subject: id.Key(),
		Domain:  domain,
		Object:  object,
		Action:  action,
	})
}

// requireIn decides in domain and writes the refusal: a 503 for an outage and a
// 403 for a denial, and false in both cases. Both refusals go through the same
// writers the middleware uses, so an outage reached this way leaves its cause in
// the log as the middleware's does.
func (p *product) requireIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	allow, err := p.allowedIn(c, perm, domain)
	if err != nil {
		unavailable(c, outage, err)
		return false
	}
	if !allow {
		forbidden(c, denied, perm)
		return false
	}
	return true
}

// registerMetrics reports the decision counts through the metric capability,
// when the assembly carries one. The capability is optional: reporting the
// counts changes no decision, so its absence changes none either, and the counts
// stay in the process.
//
// The counter is observable. The counts are the layer's own atomics, read when a
// collection asks for them, so a request pays one atomic add for being counted.
// Every outcome is observed on every collection, the empty ones included, which
// gives a series a first point before its first request.
func (p *product) registerMetrics() error {
	if p.meter == nil {
		return nil
	}
	meter := p.meter.Meter(meterName)
	counter, err := meter.Int64ObservableCounter(decisionsInstrument,
		metric.WithUnit("{decision}"),
		metric.WithDescription("Authorization decisions, by the outcome each request got"))
	if err != nil {
		return fmt.Errorf("%screating the %s counter: %w", ModuleName, decisionsInstrument, err)
	}
	attrs := make([]metric.ObserveOption, outcomeCount)
	for o := range attrs {
		attrs[o] = metric.WithAttributes(attribute.String(outcomeAttribute, outcome(o).String()))
	}
	p.metrics, err = meter.RegisterCallback(func(_ context.Context, obs metric.Observer) error {
		for o := range p.counts {
			obs.ObserveInt64(counter, p.counts[o].Load(), attrs[o])
		}
		return nil
	}, counter)
	if err != nil {
		return fmt.Errorf("%sregistering the %s counter: %w", ModuleName, decisionsInstrument, err)
	}
	return nil
}

// dropMeter withdraws the counter's callback, so that a meter provider outliving
// this module stops reading a product that has been closed.
func (p *product) dropMeter() error {
	if p.metrics == nil {
		return nil
	}
	if err := p.metrics.Unregister(); err != nil {
		return fmt.Errorf("%swithdrawing the %s counter: %w", ModuleName, decisionsInstrument, err)
	}
	return nil
}
