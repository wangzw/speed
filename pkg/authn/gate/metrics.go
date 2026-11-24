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

// conclusionsInstrument and outcomeAttribute are frozen: a dashboard or an alert
// rule written against them stops matching when either changes.
const (
	conclusionsInstrument = ModuleName + ".conclusions"
	outcomeAttribute      = "outcome"
)

// meterName is the name of the meter the counter is created on. It is the
// capability directory's name, the one every module of it reports under, and not
// this module's: the instrument's own name already says which module counts.
const meterName = "authn"

// The buckets a conclusion lands in, indexed by verdict.Outcome with the last one
// for a credential-less request that passed on an optional route; the set is
// exhaustive and exclusive, and a bucket name is never renamed.
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

// takeMeter reports the conclusion counts through the metric capability when the
// assembly carries one; absent, the counts stay in the process. Any other failure
// to take it up or register the instrument stops the startup.
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

// dropMeter withdraws the counter's callback, so a meter provider that outlives
// the gateway stops reading a closed product. It runs in Close.
func (g *gateway) dropMeter() error {
	if g.metrics == nil {
		return nil
	}
	if err := g.metrics.Unregister(); err != nil {
		return fmt.Errorf("%swithdrawing the %s counter: %w", diagnosticPrefix, conclusionsInstrument, err)
	}
	return nil
}
