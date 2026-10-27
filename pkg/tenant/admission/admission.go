// Package admission carries the layer that decides whether an identified caller
// may enter the tenant its request resolved to.
//
// It answers three questions the other layers cannot. Authentication knows who
// the caller is and not which tenant the request is for; tenant resolution knows
// the tenant and does not read the caller. This layer reads both: whether the
// account is a member of the tenant, whether it meets the tenant's own
// requirements of how its members authenticate, and whether an impersonating
// identity is confined to this tenant. It does not decide what a member may do
// inside — that is the permission model's — and it never answers 403.
//
// It is a module of the tenant directory and not of the authentication one
// because it reads an identity and a tenant context at once, and the
// authentication tree imports nothing of the tenant tree. It reaches the
// identity system through the interfaces of the authn root package alone.
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

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose tenant requests are checked.
const ModuleName = "tenant.admission"

// ConfigNamespace is where this module's input items are mounted, and the prefix
// of its settings. It is this module's own name, as a registering module's
// namespace always is.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers. It names the capability directory
// rather than the module, and the module follows it in the line itself.
const diagnosticPrefix = "tenant: "

// The names of the metric: the counter, the meter it is created on, and the
// attribute that tells the outcomes apart. A dashboard written against them
// stops matching when one changes.
const (
	decisionsInstrument = ModuleName + ".decisions"
	outcomeAttribute    = "outcome"
	meterName           = "tenant"
)

// typePrefix is what the problem type of a step-up answer starts with; the
// authentication capability's problem types are in this namespace.
const typePrefix = "urn:speed:authn:"

// insufficientUserAuthentication is the error of a step-up challenge for a
// stronger local authentication, as RFC 9470 names it.
const insufficientUserAuthentication = "insufficient_user_authentication"

// ssoRequired is the problem type of a step-up challenge that asks for a fresh
// sign-in through the tenant's identity provider. A local factor cannot satisfy
// it, so it is told apart from insufficientUserAuthentication rather than
// answered with that code.
const ssoRequired = "sso_required"

// The values acr_values takes in a step-up challenge: the number of the
// multi-factor level, as the access token's acr writes it, and the word that
// asks for a sign-in through the tenant's identity provider.
const (
	acrMultiFactor = "2"
	acrSSO         = "sso"
)

// The module registers itself. Importing this package is what puts the
// admission layer in a binary's available set; a host that never imports it
// carries none, and the requests of its tenants are then checked by whatever the
// host wrote instead, or by nothing.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers the ordering anchor and the confinement guard, each exclusively:
// two admission layers on one endpoint would leave it unsaid which of them let a
// request in. A second provider of either fails the startup.
//
// It requires the memberships and the resolution layer, and neither is optional:
// without the first there is no fact to check, and without the second no tenant
// to check it against. Both are delivered exclusively by other modules, so the
// dependency closure does not pull a provider into a command; a command that
// names this module and not theirs ends at dependency resolution, before
// anything is constructed.
//
// The audit capability is declared as an optional requirement, so that a command
// that names it orders the audit module first; going without it is the legal
// assembly where a relaxation is written to the log and no record is kept.
//
// Prepare states StateEnabled and never StateAuto: StateAuto is the cell the
// resolution of an exclusive capability disables when a second provider claims
// it, and this module must fail that startup instead of giving way. Whether the
// layer runs is a decision of the command tree.
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

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for the module, and the callbacks leave it
// alone: no layer is registered on any endpoint, no event subscribed to, and
// what the instance holds is released by whoever supplied it. Taking whatever
// carries the product's methods for the product is the shape to reject: a
// wrapper a host writes around it carries every one of them, and the callbacks
// would then register the layer on the strength of a type that is not the
// product. The price is that such an assembly has no layer on the chain and the
// registry says it has one; the design lists that among what it does not
// guarantee.
func productOf(instance any) (*layer, bool) {
	l, ok := instance.(*layer)
	return l, ok
}

// layer is this module's product. It implements tenant.Admission and
// authn.ScopeGuard.
type layer struct {
	endpoints []string
	guarded   map[string]struct{}

	memberships authn.Memberships

	// The rest is settled in wire, which the driver runs once before any
	// request is served; request goroutines read it afterwards, and that order
	// is what makes reading it without a lock safe.
	providers authn.SSOProviders
	decider   authn.Decider
	store     settings.Store
	audit     audit.Audit
	bus       event.Bus
	logger    *slog.Logger

	cache *decisionCache

	counts  [outcomeCount]atomic.Int64
	metrics metric.Registration

	// mu guards the one record of a setting that could not be read, which
	// keeps the Error line from being written for every request.
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
		endpoints:   cfg.endpoints,
		guarded:     guarded,
		memberships: memberships,
		logger:      log.Default(),
		cache:       newDecisionCache(time.Now),
	}
}

// AdmissionLayer implements tenant.Admission. It marks the type and does
// nothing.
func (*layer) AdmissionLayer() {}

// Covers implements authn.ScopeGuard: the layer guards exactly the endpoints it
// was configured with. The list holds concrete names only, so the answer is a
// membership test and never a pattern match, and a name that is not listed
// is not covered however it is spelled.
func (l *layer) Covers(endpoint string) bool {
	_, ok := l.guarded[endpoint]
	return ok
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
	return zero, false, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
}

// wire takes up what the layer reads, registers it on every endpoint it guards,
// subscribes to the change event and writes the startup diagnostics.
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
		// A relaxation is only watchable where the settings are in hand: with no
		// store there is no setting and no change event to follow.
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

// onMembershipChanged drops what the layer remembers about the account in the
// tenant the event names. It only drops: the event is a hint that a membership
// changed, the authority is the table, and the next request reads it again. An
// event is delivered at least once and in no order, and dropping an entry that is
// not there is no change, so the handler is idempotent. One that cannot be read
// down to an account and a tenant drops everything, because keeping an entry the
// event was about is the failure and dropping an extra one is a read.
func (l *layer) onMembershipChanged(_ context.Context, evt event.Event) error {
	var payload authn.MembershipChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil || payload.AccountID == "" || evt.TenantID == "" {
		l.cache.clear()
		return nil
	}
	l.cache.forget(cacheKey{account: authn.AccountID(payload.AccountID), tenant: tenant.ID(evt.TenantID)})
	return nil
}

// takeMeter reports the decision counts through the metric capability, when the
// assembly carries one. The capability is optional: reporting the counts changes
// no decision, so its absence changes none either, and the counts stay in the
// process. Any other failure to take it up is carried out and stops the startup.
//
// The counter is observable. The counts are the layer's own atomics, read when a
// collection asks for them, so a request pays one atomic add for being counted.
// Every outcome is observed on every collection, the empty ones included, which
// gives a series a first point before its first request.
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

// dropMeter withdraws the counter's callback, so that a meter provider outliving
// this layer stops reading a product that has been closed.
func (l *layer) dropMeter() error {
	if l.metrics == nil {
		return nil
	}
	if err := l.metrics.Unregister(); err != nil {
		return fmt.Errorf("%swithdrawing the %s counter: %w", diagnosticPrefix, decisionsInstrument, err)
	}
	return nil
}

// middlewareFor builds the layer registered on one endpoint.
//
// Provides carries the token the descriptor delivers the capability under: the
// only anchor another layer has for placing itself inside or outside admission.
// The layer declares the authorizing stage and no constraint of its own. That
// stage runs after identity and tenant are settled, which this layer reads, and
// inside the recording and the throttling stages, so a refusal made here is
// audited and counted by the stage order rather than by a preference.
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

// conclude counts a decision, records a refusal and writes its answer.
//
// Every request is counted once, under the outcome it got, and every refusal
// leaves one line. The line goes through the logger the request's context
// carries, so it has the fields the layers outside bound on it — the trace, the
// account and, for an impersonation, the actor from the gateway, the tenant from
// the resolution layer — and adds the class of the cause alone. The two kinds of
// refusal that mean something is wrong rather than someone is refused, a request
// with no identity where one is needed and a decision that could not be made,
// are written at Error.
//
// The answer carries the status and nothing that tells the refusals apart: a
// 404 is a 404 whether the account is not a member, the tenant is not the one
// its impersonation is for, or the permission model said no.
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

// stepUpDescription is the error_description of a step-up challenge. It names
// no requirement of the tenant and nothing of the account.
const stepUpDescription = "A stronger or more recent authentication is required"

// stepUpChallenge is the WWW-Authenticate value of a step-up answer: the scheme
// the identity authenticated with, which the gateway stamped from the
// credential's carrier, then the error and the acr_values the client acts on.
// There is no max_age: this layer compares a level and a provider and has no
// freshness requirement to state.
//
// RFC 9470 defines the parameters for the Bearer scheme; carrying them on the
// other carriers is the authentication capability's convention.
func stepUpChallenge(scheme, acr string) string {
	return challengeScheme(scheme) + ` error="` + insufficientUserAuthentication +
		`", error_description="` + stepUpDescription + `", acr_values="` + acr + `"`
}

// challengeScheme returns scheme when it is an RFC 9110 token, and Bearer
// otherwise. The gateway stamps Identity.Challenge, so an odd value is a defect,
// and it may not put a character into a header that ends the field.
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
