// Package guard is the authorization enforcement module. It delivers four
// faces: the request-path middleware on the authorizing stage, the
// route-declaration registry behind authz.Gate, the handler-internal Guard API,
// and the authentication capability's decision seam authn.Decider.
//
// The middleware and the decider share one translation from a permission to the
// (object, action) pair the engine matches on, one declaration table, and one
// read of the tenant.
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

// ModuleName is this module's name in the registry.
const ModuleName = "authz.guard"

// The names of the metric: the counter, the meter it is created on, and the
// attribute that tells the outcomes apart.
const (
	decisionsInstrument = ModuleName + ".decisions"
	outcomeAttribute    = "outcome"
	meterName           = "authz"
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers the route-declaration
// anchor, the handler-internal Guard API and the decision seam, each
// exclusively.
//
// It requires the engine and the catalog, both delivered exclusively by the
// policy module. The HTTP entry point is taken up in Init rather than declared
// as a requirement; the logger and the meter provider are optional.
//
// Prepare states StateEnabled and never StateAuto.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authz.Enforcer)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*authz.SubjectPermissions)(nil), Optional: true},
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
			permissions, _, err := optional[authz.SubjectPermissions](reg, "the permission reader")
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the permission reader: %w", ModuleName, err)
			}
			return &product{
				enforcer:    enforcer,
				catalog:     catalog,
				permissions: permissions,
				gate:        newGate(catalog),
				cfg:         cfg,
				logger:      log.Default(),
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
				// this one never accepted.
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
// never widens it.
//
// It lives here and not in the root package because its signature takes a
// *gin.Context. Its writing method is named RequirePermission rather than
// Require because the same product also delivers authz.Gate, whose Require takes
// a method and a path.
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
	// explicitly rather than read from the request. It is for a caller whose
	// decision must not be steered by whatever tenant the request resolved to.
	RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool
}

// product is this module's product. It implements authn.Decider, the
// handler-internal Guard, and holds the declaration table the middleware reads.
type product struct {
	enforcer    authz.Enforcer
	catalog     authz.Catalog
	permissions authz.SubjectPermissions
	gate        *gate
	cfg         resolved
	logger      *slog.Logger

	meter   observability.MeterProvider
	counts  [outcomeCount]atomic.Int64
	metrics metric.Registration
}

var (
	_ authn.Decider = (*product)(nil)
	_ Guard         = (*product)(nil)
	_ authz.Gate    = (*product)(nil)
)

// Require implements authz.Gate.
func (p *product) Require(method, path string, perm authz.Permission) {
	p.gate.Require(method, path, perm)
}

// Open implements authz.Gate.
func (p *product) Open(method, path string) { p.gate.Open(method, path) }

// Covered implements authz.Gate.
func (p *product) Covered(method, path string) bool { return p.gate.Covered(method, path) }

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok
}

// optional takes up a capability that may be absent. Absence is the legal
// configuration and leaves the zero value; any other failure stops the startup.
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
// endpoint and writes the startup diagnostics. A name the entry point does not
// declare is ErrUnknownEndpoint and stops the startup. The process's translation
// set is resolved here, as it is by the policy module.
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

func undeclaredName(m undeclaredMode) string {
	if m == undeclaredOpen {
		return openValue
	}
	return closedValue
}

// middlewareFor builds the layer registered on one endpoint. The layer declares
// the authorizing stage and sits after the tenant admission anchor.
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

// enforce decides a route that declared a required permission. An engine error
// is an outage (503), a false is a denial (403), a true passes.
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
		// express; the safe answer is a denial.
		p.conclude(c, decision{outcome: denied, permission: perm}, realm)
		return
	}
	allow, err := p.enforcer.Allowed(ctx, authz.Query{
		Subject: string(id.Account()),
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
		unavailable(c, d.outcome, d.cause)
	}
}

// domainOf reads the policy domain a handler-internal decision runs in when the
// domain is not named.
func domainOf(c *gin.Context) authz.Domain {
	if tenantID, ok := tenant.FromContext(c.Request.Context()); ok {
		return authz.Domain(tenantID)
	}
	return authz.PlatformDomain
}

// Allowed implements Guard.
func (p *product) Allowed(c *gin.Context, perm authz.Permission) (bool, error) {
	return p.allowedIn(c, perm, domainOf(c))
}

// RequirePermission implements Guard.
func (p *product) RequirePermission(c *gin.Context, perm authz.Permission) bool {
	return p.requireIn(c, perm, domainOf(c))
}

// RequirePermissionIn implements Guard.
func (p *product) RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	return p.requireIn(c, perm, domain)
}

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
		Subject: string(id.Account()),
		Domain:  domain,
		Object:  object,
		Action:  action,
	})
}

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
// when the assembly carries one. The capability is optional and reporting the
// counts changes no decision.
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

// dropMeter withdraws the counter's callback, so that a meter provider
// outliving this module stops reading a product that has been closed.
func (p *product) dropMeter() error {
	if p.metrics == nil {
		return nil
	}
	if err := p.metrics.Unregister(); err != nil {
		return fmt.Errorf("%swithdrawing the %s counter: %w", ModuleName, decisionsInstrument, err)
	}
	return nil
}
