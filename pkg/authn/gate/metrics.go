package gate

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// conclusionsInstrument is the counter the buckets are reported through, and
// outcomeAttribute the attribute that tells the buckets apart. A dashboard or an
// alert rule written against them stops matching when either changes, so they
// are fixed here the way the bucket names are.
const (
	conclusionsInstrument = ModuleName + ".conclusions"
	outcomeAttribute      = "outcome"
)

// meterName is the name of the meter the counter is created on. It is the
// capability directory's name, the one every module of it reports under, and not
// this module's: the instrument's own name already says which module counts.
const meterName = "authn"

// The buckets a conclusion lands in. The first four are the outcomes of a
// verdict, indexed by verdict.Outcome, and the last is a request that carried no
// credential and passed on an optional route. The set is exhaustive and the
// buckets exclude each other: every conclusion about a credential or about the
// absence of one lands in exactly one. A request on an exempt route and one on
// an endpoint this gateway does not guard are not conclusions and land in none.
// The names are frozen, a name can be added and none is renamed.
const (
	bucketAnonymous = int(verdict.Unavailable) + 1
	bucketCount     = bucketAnonymous + 1
)

func bucketName(bucket int) string {
	if bucket == bucketAnonymous {
		return "anonymous"
	}
	return verdict.Outcome(bucket).String()
}

// takeMeter reports the conclusion counts through the metric capability, when
// the assembly carries one.
//
// The metric capability is not a dependency of this module, on the terms the
// logging capability is not: it is taken up in Init, where every instance
// exists, and reporting the counts changes no authentication behaviour, so its
// absence changes none either. Absent, the counts stay in the process and the
// log lines are the only record of each conclusion. Any other failure to take
// it up, or to register the instrument, is carried out and stops the startup
// rather than leaving the counts unreported without a word.
//
// The counter is observable: the buckets are the gateway's own atomics, read
// when a collection asks for them, so a request pays nothing for being counted
// here. Every bucket is observed on every collection, the empty ones included,
// which is what gives a series a first point before its first conclusion.
func (g *gateway) takeMeter(reg *core.Registry) error {
	provider, err := core.Resolve[observability.MeterProvider](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil
		}
		return fmt.Errorf("%staking up the metric capability to report the conclusion counts "+
			"through: %w", diagnosticPrefix, err)
	}

	meter := provider.Meter(meterName)
	counter, err := meter.Int64ObservableCounter(conclusionsInstrument,
		metric.WithUnit("{conclusion}"),
		metric.WithDescription("Request-time authentication conclusions, by the bucket each "+
			"landed in"))
	if err != nil {
		return fmt.Errorf("%screating the %s counter: %w", diagnosticPrefix, conclusionsInstrument, err)
	}

	buckets := make([]metric.ObserveOption, len(g.counts))
	for i := range g.counts {
		buckets[i] = metric.WithAttributes(attribute.String(outcomeAttribute, bucketName(i)))
	}
	registration, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for i := range g.counts {
			o.ObserveInt64(counter, g.counts[i].Load(), buckets[i])
		}
		return nil
	}, counter)
	if err != nil {
		return fmt.Errorf("%sregistering the %s counter: %w", diagnosticPrefix, conclusionsInstrument, err)
	}
	g.metrics = registration
	return nil
}

// dropMeter withdraws the counter's callback, so that a meter provider outliving
// this gateway stops reading a product that has been closed. It runs in Close.
// Whether the metric capability's own final collection lands before it follows
// from the construction order, which this module does not declare; a
// collection that lands after it finds the counter gone and reports the rest.
func (g *gateway) dropMeter() error {
	if g.metrics == nil {
		return nil
	}
	if err := g.metrics.Unregister(); err != nil {
		return fmt.Errorf("%swithdrawing the %s counter: %w", diagnosticPrefix, conclusionsInstrument, err)
	}
	return nil
}
