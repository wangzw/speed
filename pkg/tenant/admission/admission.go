// Package admission carries the layer that decides whether an identified caller may enter the tenant its
// request resolved to, by membership, the tenant's authentication requirements, and an impersonation's
// confinement. It does not decide what a member may do inside and never answers 403.
package admission

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.admission"

// ConfigNamespace is where this module's input items and settings are mounted.
const ConfigNamespace = ModuleName

const diagnosticPrefix = "tenant: "

const (
	decisionsInstrument = ModuleName + ".decisions"
	outcomeAttribute    = "outcome"
	meterName           = "tenant"
)

const typePrefix = "urn:speed:authn:"

const insufficientUserAuthentication = "insufficient_user_authentication"

const ssoRequired = "sso_required"

const (
	acrMultiFactor = "2"
	acrSSO         = "sso"
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers the ordering anchor, the confinement guard
// and authn.TenantMFARequirement exclusively; it requires the memberships and resolution layer, and
// optionally audit. Prepare states StateEnabled, never StateAuto.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.Memberships)(nil)},
			{Token: (*tenant.Resolution)(nil)},
			{Token: (*audit.Audit)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*tenant.Admission)(nil), Exclusive: true},
			{Token: (*authn.ScopeGuard)(nil), Exclusive: true},
			{Token: (*authn.TenantMFARequirement)(nil), Exclusive: true},
		},
		Resources: []any{schema(), settingsSchema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return prepare(reader)
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return nil, err
			}
			return newLayer(cfg, memberships), nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			l, ok := productOf(instance)
			if !ok {
				return nil
			}
			return l.wire(reg)
		},

		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			l, ok := productOf(instance)
			if !ok {
				return nil
			}
			return l.dropMeter()
		},
	}
}

func productOf(instance any) (*layer, bool) {
	l, ok := instance.(*layer)
	return l, ok
}

type layer struct {
	endpoints        []string
	ssoExemptTenants []string
	guarded          map[string]struct{}

	memberships authn.Memberships

	providers authn.SSOProviders
	decider   authn.Decider
	store     settings.Store
	audit     audit.Audit
	bus       event.Bus
	logger    *slog.Logger

	cache *decisionCache

	counts  [outcomeCount]atomic.Int64
	metrics metric.Registration

	mu          sync.Mutex
	reportedTTL string
}

var (
	_ tenant.Admission = (*layer)(nil)
	_ authn.ScopeGuard = (*layer)(nil)
)

func newLayer(cfg resolved, memberships authn.Memberships) *layer {
	guarded := make(map[string]struct{}, len(cfg.endpoints))
	for _, name := range cfg.endpoints {
		guarded[name] = struct{}{}
	}
	return &layer{
		endpoints:        cfg.endpoints,
		ssoExemptTenants: cfg.ssoExemptTenants,
		guarded:          guarded,
		memberships:      memberships,
		logger:           log.Default(),
		cache:            newDecisionCache(time.Now),
	}
}

// AdmissionLayer implements tenant.Admission. It marks the type and does
// nothing.
func (*layer) AdmissionLayer() {}

// Covers implements authn.ScopeGuard: the layer guards exactly the endpoints it
// was configured with, by membership and never by pattern.
func (l *layer) Covers(endpoint string) bool {
	_, ok := l.guarded[endpoint]
	return ok
}

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
	return zero, false, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
}

func (l *layer) wire(reg *core.Registry) error {
	if delivered, found, err := optional[log.Logger](reg, "the logger"); err != nil {
		return err
	} else if found {
		l.logger = delivered.Named(ModuleName)
	}

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, tenant.ErrNoRouter)
	}

	var have bool
	if l.providers, have, err = optional[authn.SSOProviders](reg, "the identity provider registry"); err != nil {
		return err
	} else if !have {
		l.providers = nil
	}
	if l.decider, have, err = optional[authn.Decider](reg, "the decider"); err != nil {
		return err
	} else if !have {
		l.decider = nil
	}
	if l.store, have, err = optional[settings.Store](reg, "the settings"); err != nil {
		return err
	} else if !have {
		l.store = nil
	}
	if l.audit, have, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	} else if !have {
		l.audit = nil
	}

	for _, name := range l.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s%s: %w: %q: %w", diagnosticPrefix, ModuleName,
				tenant.ErrUnknownEndpoint, name, err)
		}
		endpoint.Use(l.middlewareFor(name))
	}

	if bus, found, err := optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	} else if found {
		l.bus = bus
	}
	if l.bus != nil {
		l.bus.Subscribe(authn.EventMembershipChanged, ModuleName, l.onMembershipChanged)
		if l.store != nil {
			l.bus.Subscribe(settings.ChangedEventType, ModuleName, l.onSettingsChanged)
		}
	}
	if err := l.takeMeter(reg); err != nil {
		return err
	}

	l.logger.Info(diagnosticPrefix+ModuleName+": admission in force",
		"guarded", l.endpoints,
		"identity_providers", l.providers != nil,
		"decider", l.decider != nil,
		"settings", l.store != nil)
	if l.providers == nil && l.store != nil {
		l.logger.Warn(diagnosticPrefix + ModuleName + ": the registry of identity providers is " +
			"absent while " + requireSSOKey + " can be switched on in the settings. A tenant " +
			"that switches it on has every request refused, because no provider can be " +
			"placed in it, and the assurance of a multi-factor level an identity provider " +
			"declared is counted as one factor")
	}
	return nil
}

func (l *layer) onMembershipChanged(_ context.Context, evt event.Event) error {
	var payload authn.MembershipChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil || payload.AccountID == "" || evt.TenantID == "" {
		l.cache.clear()
		return nil
	}
	l.cache.forget(cacheKey{account: authn.AccountID(payload.AccountID), tenant: tenant.ID(evt.TenantID)})
	return nil
}

func (l *layer) takeMeter(reg *core.Registry) error {
	provider, found, err := optional[observability.MeterProvider](reg, "the metric capability")
	if err != nil || !found {
		return err
	}
	meter := provider.Meter(meterName)
	counter, err := meter.Int64ObservableCounter(decisionsInstrument,
		metric.WithUnit("{decision}"),
		metric.WithDescription("Admission decisions, by the outcome each request got"))
	if err != nil {
		return fmt.Errorf("%screating the %s counter: %w", diagnosticPrefix, decisionsInstrument, err)
	}
	attrs := make([]metric.ObserveOption, outcomeCount)
	for o := range attrs {
		attrs[o] = metric.WithAttributes(attribute.String(outcomeAttribute, outcome(o).String()))
	}
	l.metrics, err = meter.RegisterCallback(func(_ context.Context, obs metric.Observer) error {
		for o := range l.counts {
			obs.ObserveInt64(counter, l.counts[o].Load(), attrs[o])
		}
		return nil
	}, counter)
	if err != nil {
		return fmt.Errorf("%sregistering the %s counter: %w", diagnosticPrefix, decisionsInstrument, err)
	}
	return nil
}

func (l *layer) dropMeter() error {
	if l.metrics == nil {
		return nil
	}
	if err := l.metrics.Unregister(); err != nil {
		return fmt.Errorf("%swithdrawing the %s counter: %w", diagnosticPrefix, decisionsInstrument, err)
	}
	return nil
}

func (l *layer) middlewareFor(endpoint string) speedhttp.Middleware {
	realm := quoteRealm(endpoint)
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*tenant.Admission)(nil)},
		Stage:    speedhttp.StageAuthorize,
		Handler: func(c *gin.Context) {
			d := l.decide(c.Request.Context())
			l.conclude(c, d, realm)
			if d.outcome.passes() {
				c.Next()
			}
		},
	}
}

func (l *layer) conclude(c *gin.Context, d decision, realm string) {
	l.counts[d.outcome].Add(1)
	if d.outcome.passes() {
		return
	}
	logger := log.FromContext(c.Request.Context())
	switch d.outcome {
	case noIdentity:
		logger.Error(diagnosticPrefix+ModuleName+": a request with no identity reached a route that "+
			"needs one. The gateway is not in front of this endpoint or does not guard it",
			outcomeAttribute, d.outcome.String())
	case unavailable:
		logger.Error(diagnosticPrefix+ModuleName+": the request could not be decided",
			outcomeAttribute, d.outcome.String(), "error", d.cause)
	default:
		logger.Info(diagnosticPrefix+ModuleName+": request refused", outcomeAttribute, d.outcome.String())
	}

	problem := speedhttp.Problem{Status: d.outcome.status()}
	switch d.outcome {
	case stepUpMFA, stepUpSSO:
		acr := acrMultiFactor
		problem.Type = typePrefix + insufficientUserAuthentication
		if d.outcome == stepUpSSO {
			acr = acrSSO
			problem.Type = typePrefix + ssoRequired
		}
		c.Header("WWW-Authenticate", stepUpChallenge(d.challenge, acr))
	case noIdentity:
		c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	}
	speedhttp.Fail(c, problem)
}

const stepUpDescription = "A stronger or more recent authentication is required"

func stepUpChallenge(scheme, acr string) string {
	return challengeScheme(scheme) + ` error="` + insufficientUserAuthentication +
		`", error_description="` + stepUpDescription + `", acr_values="` + acr + `"`
}

func challengeScheme(scheme string) string {
	if scheme == "" {
		return "Bearer"
	}
	for i := 0; i < len(scheme); i++ {
		ch := scheme[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			strings.IndexByte("!#$%&'*+-.^_`|~", ch) >= 0:
		default:
			return "Bearer"
		}
	}
	return scheme
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped because it would
// start another header.
func quoteRealm(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch ch := name[i]; ch {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case '\r', '\n':
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}
