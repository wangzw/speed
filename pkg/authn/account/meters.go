package account

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/observability"
)

// The meter every module of the capability directory reports under, and the
// instruments and attributes this module reports through. A dashboard or an alert
// rule written against a name stops matching when it changes, so they are fixed
// here.
const (
	meterName = "authn"

	verificationsInstrument = "authn.password.verifications"
	deriveInstrument        = "authn.password.derive.duration"
	cleanupInstrument       = "authn.cleanup.failures"
	engagementsInstrument   = "authn.throttle.engagements"
	auditFailuresInstrument = "authn.audit.write-failures"

	outcomeAttribute   = "outcome"
	algorithmAttribute = "algorithm"
	hookAttribute      = "hook"
	scopeAttribute     = "scope"
	mandatoryAttribute = "mandatory"
)

// The outcomes of a password verification, as the instrument's attribute takes
// them.
const (
	outcomeOK       = "ok"
	outcomeBad      = "bad"
	outcomeBusy     = "busy"
	outcomeUpgraded = "upgraded"
)

// The hooks a cleanup failure is told apart by. The first three are the modules
// that implement AccountLifecycle today, and the fourth is the revocation of a
// service secret's session; a hook this module cannot name reads other.
const (
	hookSession = "session"
	hookMFA     = "mfa"
	hookSMS     = "sms"
	hookSecret  = "secret"
	hookOther   = "other"
)

// meters is the instruments this module reports through. The zero value reports
// nothing, which is the state of an assembly with no metric capability, and every
// method is safe on it.
type meters struct {
	verifications metric.Int64Counter
	derive        metric.Float64Histogram
	cleanup       metric.Int64Counter
	engagements   metric.Int64Counter
	auditFailures metric.Int64Counter
}

// newMeters creates the instruments on the meter of the capability directory.
func newMeters(provider observability.MeterProvider) (meters, error) {
	meter := provider.Meter(meterName)
	var m meters
	var err error
	if m.verifications, err = meter.Int64Counter(verificationsInstrument,
		metric.WithUnit("{verification}"),
		metric.WithDescription("Password verifications, by outcome")); err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", verificationsInstrument, err)
	}
	if m.derive, err = meter.Float64Histogram(deriveInstrument,
		metric.WithUnit("s"),
		metric.WithDescription("How long one password derivation took, by algorithm")); err != nil {
		return meters{}, fmt.Errorf("creating the %s histogram: %w", deriveInstrument, err)
	}
	if m.cleanup, err = meter.Int64Counter(cleanupInstrument,
		metric.WithUnit("{failure}"),
		metric.WithDescription("Lifecycle hooks that failed after an account or a service secret changed state")); err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", cleanupInstrument, err)
	}
	if m.engagements, err = meter.Int64Counter(engagementsInstrument,
		metric.WithUnit("{engagement}"),
		metric.WithDescription("Back-offs that began, by the scope of the key")); err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", engagementsInstrument, err)
	}
	if m.auditFailures, err = meter.Int64Counter(auditFailuresInstrument,
		metric.WithUnit("{failure}"),
		metric.WithDescription("Audit rows that could not be written, by whether the action required one")); err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", auditFailuresInstrument, err)
	}
	return m, nil
}

func (m meters) verified(ctx context.Context, outcome string) {
	if m.verifications != nil {
		m.verifications.Add(ctx, 1, metric.WithAttributes(attribute.String(outcomeAttribute, outcome)))
	}
}

func (m meters) derived(ctx context.Context, algorithm password.Algorithm, seconds float64) {
	if m.derive != nil {
		m.derive.Record(ctx, seconds, metric.WithAttributes(attribute.String(algorithmAttribute, string(algorithm))))
	}
}

func (m meters) cleanupFailed(ctx context.Context, hook string) {
	if m.cleanup != nil {
		m.cleanup.Add(ctx, 1, metric.WithAttributes(attribute.String(hookAttribute, hook)))
	}
}

func (m meters) engaged(ctx context.Context, scope authn.ThrottleScope) {
	if m.engagements != nil {
		m.engagements.Add(ctx, 1, metric.WithAttributes(attribute.String(scopeAttribute, string(scope))))
	}
}

func (m meters) auditFailed(ctx context.Context, mandatory bool) {
	if m.auditFailures != nil {
		m.auditFailures.Add(ctx, 1, metric.WithAttributes(attribute.Bool(mandatoryAttribute, mandatory)))
	}
}

// hookName is what a lifecycle implementation reports as in the cleanup counter:
// the last element of the package it is declared in, when that is one of the
// three modules that hold account data, and other for any other.
func hookName(h authn.AccountLifecycle) string {
	t := reflect.TypeOf(h)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return hookOther
	}
	path := t.PkgPath()
	name := path[strings.LastIndex(path, "/")+1:]
	switch name {
	case hookSession, hookMFA, hookSMS:
		return name
	}
	return hookOther
}
