package otel_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/otel"
)

// errShutDown is what a destination answers once it has been released, the way
// the upstream exporters do: an export after Shutdown is refused, not queued.
var errShutDown = errors.New("exporter shutdown")

// destination is one backend's receiving end, shared by its two exporters so
// that a case can read what reached it and in which order it was released.
type destination struct {
	mu      sync.Mutex
	spans   []string
	metrics []string
	// released lists the halves in the order they were shut down, and an
	// export that arrived after its half was released is refused and counted
	// in late.
	released []string
	late     int
}

func (d *destination) isReleased(half string) bool {
	for _, r := range d.released {
		if r == half {
			return true
		}
	}
	return false
}

type destinationSpans struct{ d *destination }

func (s destinationSpans) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.d.isReleased("span") {
		s.d.late++
		return errShutDown
	}
	for _, span := range spans {
		s.d.spans = append(s.d.spans, span.Name())
	}
	return nil
}

func (s destinationSpans) Shutdown(context.Context) error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.released = append(s.d.released, "span")
	return nil
}

type destinationMetrics struct{ d *destination }

func (m destinationMetrics) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (m destinationMetrics) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (m destinationMetrics) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	if m.d.isReleased("metric") {
		m.d.late++
		return errShutDown
	}
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			m.d.metrics = append(m.d.metrics, metric.Name)
		}
	}
	return nil
}

func (m destinationMetrics) ForceFlush(context.Context) error { return nil }

func (m destinationMetrics) Shutdown(context.Context) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	m.d.released = append(m.d.released, "metric")
	return nil
}

// backendModule is an export backend shaped the way the documentation asks one
// to be: both exporter capabilities, non-exclusive, delivered as one
// observability.Exporters, and no Close of its own.
func backendModule(name string, d *destination) core.Module {
	return core.Module{
		Name: name,
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: destinationSpans{d}, Metric: destinationMetrics{d}}, nil
		},
	}
}

// placeholderBackend is a module that declares the two exporter capabilities and
// nothing else, so an instance supplied for it stands where a backend's own
// module would. It has no New to build an instance and no Close to release one.
func placeholderBackend(name string) core.Module {
	return core.Module{
		Name: name,
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
	}
}

// recordOneOfEach is the action the cases here run while the assembly is up: it
// records one span and one metric. The configured intervals are the defaults, a
// batch timeout of seconds and a metric interval of a minute, so nothing is
// exported while it runs: whatever reaches a destination got there through the
// shutdown.
func recordOneOfEach(ctx context.Context, reg *core.Registry) error {
	mp, err := core.Resolve[observability.MeterProvider](reg)
	if err != nil {
		return err
	}
	counter, err := mp.Meter("orders").Int64Counter("orders.placed")
	if err != nil {
		return err
	}
	counter.Add(ctx, 1)

	tp, err := core.Resolve[observability.TracerProvider](reg)
	if err != nil {
		return err
	}
	_, span := tp.Tracer("orders").Start(ctx, "place")
	span.End()
	return nil
}

// driveAndRecord runs one command through Init with the given backends, records
// one span and one metric while it is up, and returns what the drive returned.
func driveAndRecord(t *testing.T, backends map[string]*destination) (*bytes.Buffer, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedConfig{serviceName: "orders"}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(buf, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	participants := []string{otel.ModuleName}
	for name, d := range backends {
		reg.Register(backendModule(name, d))
		participants = append(participants, name)
	}
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
		Action:       recordOneOfEach,
	})
	return buf, err
}

// TestCloseDeliversTheLastBatchOfBothSignals is the case for the flush the
// module promises at shutdown. A backend hands one product for both
// capabilities, so the two providers share one Shutdown: releasing the backend
// when the first provider is done would leave the second provider's final
// export — the metrics recorded since the last interval — landing on an
// exporter that has already gone, and the drive would fail with it.
func TestCloseDeliversTheLastBatchOfBothSignals(t *testing.T) {
	d := &destination{}
	buf, err := driveAndRecord(t, map[string]*destination{"observability.test-backend": d})
	if err != nil {
		t.Fatalf("a graceful stop failed: %v\nthe module logged:\n%s", err, buf)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.spans) != 1 || d.spans[0] != "place" {
		t.Errorf("the destination received spans %v, want the one span ended before the stop", d.spans)
	}
	if !contains(d.metrics, "orders.placed") {
		t.Errorf("the destination received metrics %v, want orders.placed: the final collection "+
			"of the meter provider is the only export the metric reached", d.metrics)
	}
	if d.late != 0 {
		t.Errorf("%d export(s) reached the destination after it was released", d.late)
	}
	if !d.isReleased("span") || !d.isReleased("metric") {
		t.Errorf("the destination released %v, want both halves released by the stop", d.released)
	}
}

// TestCloseDeliversTheLastBatchToEveryBackend is the same promise with two
// destinations, each of which is collected once per capability and must still
// receive both signals before either is released.
func TestCloseDeliversTheLastBatchToEveryBackend(t *testing.T) {
	first, second := &destination{}, &destination{}
	buf, err := driveAndRecord(t, map[string]*destination{
		"observability.test-first":  first,
		"observability.test-second": second,
	})
	if err != nil {
		t.Fatalf("a graceful stop failed: %v\nthe module logged:\n%s", err, buf)
	}
	for name, d := range map[string]*destination{"first": first, "second": second} {
		d.mu.Lock()
		if len(d.spans) != 1 || !contains(d.metrics, "orders.placed") || d.late != 0 {
			t.Errorf("the %s destination received spans %v and metrics %v with %d late export(s), "+
				"want one span, orders.placed and none late", name, d.spans, d.metrics, d.late)
		}
		d.mu.Unlock()
	}
}

// TestASuppliedBackendIsReleasedByTheAssembler is the same promise for a backend
// whose instance the host supplies instead of naming its module on the command
// tree. The placeholder module the instance is supplied for has no Close and no
// New, so the assembler is the only thing that can release what the host
// supplied: it has to collect the exporters from the supplied instance, make its
// final export of both signals through them, and release them after that.
func TestASuppliedBackendIsReleasedByTheAssembler(t *testing.T) {
	const backend = "observability.test-supplied"
	d := &destination{}
	buf := &bytes.Buffer{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedConfig{serviceName: "orders"}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(buf, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(placeholderBackend(backend))
	reg.Supply(&observability.Exporters{Span: destinationSpans{d}, Metric: destinationMetrics{d}}, backend)

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName},
		Action:       recordOneOfEach,
	})
	if err != nil {
		t.Fatalf("a graceful stop over a backend the host supplied failed: %v\nthe module logged:\n%s", err, buf)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.spans) != 1 || d.spans[0] != "place" {
		t.Errorf("the supplied destination received spans %v, want the one span ended before the stop", d.spans)
	}
	if !contains(d.metrics, "orders.placed") {
		t.Errorf("the supplied destination received metrics %v, want orders.placed: the final collection "+
			"of the meter provider is the only export the metric reached", d.metrics)
	}
	if d.late != 0 {
		t.Errorf("%d export(s) reached the supplied destination after it was released", d.late)
	}
	if !d.isReleased("span") || !d.isReleased("metric") {
		t.Errorf("the supplied destination released %v, want both halves released by the stop: nothing but "+
			"the assembler releases an instance the host supplied for a module with no Close", d.released)
	}
}

// boundedConfig gives this module a service name and a short shutdown bound.
type boundedConfig struct{ bound time.Duration }

func (c boundedConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		cfg := target.(*otel.Config)
		cfg.ServiceName = "orders"
		cfg.ShutdownTimeout = c.bound
	}
	return nil
}

// stuckSpans is a destination whose export ignores its context and returns only
// once the case lets it go, so neither the cutoff nor the bound ends it.
type stuckSpans struct{ release chan struct{} }

func (s stuckSpans) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	<-s.release
	return nil
}
func (stuckSpans) Shutdown(context.Context) error { return nil }

// TestAShutdownPastItsBoundFailsTheStop pins what a tracer provider's flush
// that does not finish within shutdown-timeout reports: Close stops waiting at
// the bound and returns the deadline, so the drive fails and the host sees that
// the stop did not complete. What was lost is not reported. The span export
// ignores its context, which is the overrun the cutoff does not reach: an
// export that honours its context is cut short inside the bound instead. The
// export is let go only once the case is done, so a shutdown that waited for it
// would never return, and the drive runs on a goroutine of its own that the
// case gives up on.
func TestAShutdownPastItsBoundFailsTheStop(t *testing.T) {
	stuck := stuckSpans{release: make(chan struct{})}
	t.Cleanup(func() { close(stuck.release) })

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(boundedConfig{bound: 50 * time.Millisecond}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(core.Module{
		Name:     "observability.test-stuck",
		Provides: []core.Provision{{Token: (*observability.SpanExporter)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return stuck, nil },
	})

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- reg.Drive(context.Background(), core.Drive{
			Depth:        core.StageInit,
			Participants: []string{otel.ModuleName, "observability.test-stuck"},
			Action: func(ctx context.Context, reg *core.Registry) error {
				tp, err := core.Resolve[observability.TracerProvider](reg)
				if err != nil {
					return err
				}
				_, span := tp.Tracer("orders").Start(ctx, "place")
				span.End()
				return nil
			},
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a flush past its bound ended the drive with %v, want the deadline", err)
		}
		if elapsed := time.Since(start); elapsed > coretest.Scale(5*time.Second) {
			t.Errorf("the stop took %v against a bound of 50ms", elapsed)
		}
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("the stop did not return: the tracer provider's shutdown is not held to the bound")
	}
}

// inFlightConfig gives this module a shutdown bound of a second, a batch that
// leaves almost at once, and an export timeout far past the bound, so that an
// export the processor starts while the process serves is still in flight when
// the stop begins and nothing but the cutoff would end it inside the bound.
type inFlightConfig struct{}

const (
	inFlightBound         = time.Second
	inFlightExportTimeout = time.Minute
	// inFlightCutoffEarliest is the earliest an export in flight may be cut
	// under inFlightBound: the cutoff falls at nine tenths of it, and this
	// leaves room below that for nothing but a cutoff placed too early.
	inFlightCutoffEarliest = 800 * time.Millisecond
)

func (inFlightConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		cfg := target.(*otel.Config)
		cfg.ServiceName = "orders"
		cfg.ShutdownTimeout = inFlightBound
		cfg.Batch.BatchTimeout = 10 * time.Millisecond
		cfg.Batch.ExportTimeout = inFlightExportTimeout
	}
	return nil
}

// heldSpans is a destination whose first export waits for its context to end,
// the way a transport retrying against a collector that is down does. It says
// when that export began and when, and with what error, its context ended.
type heldSpans struct {
	began chan struct{}
	ended chan heldEnd
	once  sync.Once
}

type heldEnd struct {
	at  time.Time
	err error
}

func (s *heldSpans) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	first := false
	s.once.Do(func() {
		first = true
		close(s.began)
	})
	<-ctx.Done()
	if first {
		s.ended <- heldEnd{at: time.Now(), err: ctx.Err()}
	}
	return ctx.Err()
}

func (*heldSpans) Shutdown(context.Context) error { return nil }

// TestAnExportInFlightAtTheStopIsCutShort pins that the cutoff reaches an export
// already running when the stop begins, not only the final ones. The processor
// runs that export on a context of its own, bounded by batch.export-timeout, so
// left alone it holds the tracer provider's shutdown past shutdown-timeout and
// the stop fails with the deadline. Cut short, the export fails, the wrapper
// reports and absorbs it, and the stop succeeds inside its bound.
func TestAnExportInFlightAtTheStopIsCutShort(t *testing.T) {
	held := &heldSpans{began: make(chan struct{}), ended: make(chan heldEnd, 1)}
	logged := &lockedBuffer{}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(inFlightConfig{}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(logged, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(core.Module{
		Name:     "observability.test-held",
		Provides: []core.Provision{{Token: (*observability.SpanExporter)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return held, nil },
	})

	var stopBegan time.Time
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.test-held"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			_, span := tp.Tracer("orders").Start(ctx, "place")
			span.End()
			select {
			case <-held.began:
			case <-time.After(coretest.Scale(10 * time.Second)):
				return errors.New("the processor started no export while the process served")
			}
			stopBegan = time.Now()
			return nil
		},
	})
	stopped := time.Since(stopBegan)

	if err != nil {
		t.Errorf("a stop with an export in flight ended the drive with %v, want nil: the export is cut "+
			"short and its failure absorbed\nthe module logged:\n%s", err, logged)
	}
	select {
	case end := <-held.ended:
		if !errors.Is(end.err, context.Canceled) {
			t.Errorf("the export in flight ended with %v, want the cancellation of the cutoff", end.err)
		}
		took := end.at.Sub(stopBegan)
		// The window is a behaviour criterion: it tells a cutoff placed at nine
		// tenths of the bound from one placed at its start or not at all, so it
		// carries GATE_TIME_SCALE alone. The wrong implementations need up to
		// inFlightExportTimeout (a minute), far past the bound at any scale a
		// caller declares.
		if took >= coretest.Declared(inFlightBound) {
			t.Errorf("the export in flight ended %v into the stop, want inside the bound of %v", took, inFlightBound)
		}
		// The cutoff sits a tenth of the bound before its end, so an export that
		// ends before inFlightCutoffEarliest was cut sooner than the stop needs. The
		// window is written as a time of its own rather than derived from the
		// module's margin, so a margin that drifts turns the case red. A lower
		// bound carries no scaler: a slow machine only makes it easier to
		// satisfy, and scaling it would stop it from holding the cutoff to
		// having waited at all.
		if took < inFlightCutoffEarliest {
			t.Errorf("the export in flight ended %v into the stop, want no sooner than %v: the cutoff "+
				"leaves the exports nine tenths of the bound", took, inFlightCutoffEarliest)
		}
	default:
		t.Errorf("the export in flight had not ended when the drive returned after %v: it runs on to "+
			"batch.export-timeout (%v) unless the stop cuts it", stopped, inFlightExportTimeout)
	}
	if stopped > coretest.Declared(inFlightBound+otel.ReleaseTimeout+releaseSlack) {
		t.Errorf("the stop took %v, want at most %v", stopped, inFlightBound+otel.ReleaseTimeout+releaseSlack)
	}
	if !strings.Contains(logged.String(), "telemetry export failed") {
		t.Errorf("the module logged no export failure:\n%s\nwant the wrapper's line for the export it cut", logged)
	}
}

// slowSpans is a destination whose export honours its context and takes the
// given time to deliver, the way a reachable collector over a slow link does.
type slowSpans struct {
	takes     time.Duration
	mu        sync.Mutex
	delivered int
}

func (s *slowSpans) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	t := time.NewTimer(s.takes)
	defer t.Stop()
	select {
	case <-t.C:
		s.mu.Lock()
		defer s.mu.Unlock()
		s.delivered += len(spans)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*slowSpans) Shutdown(context.Context) error { return nil }

func (s *slowSpans) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delivered
}

// TestAFinalExportThatFitsBeforeTheCutoffIsDelivered pins where the cutoff
// falls: a final export that takes a fifth of shutdown-timeout, against a
// destination that honours its context, is delivered and the stop succeeds. A
// cutoff placed at or near the start of the stop would cut it and lose the
// batch with no failure of the stop to show for it.
func TestAFinalExportThatFitsBeforeTheCutoffIsDelivered(t *testing.T) {
	const bound = time.Second
	slow := &slowSpans{takes: bound / 5}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(boundedConfig{bound: bound}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(core.Module{
		Name:     "observability.test-slow",
		Provides: []core.Provision{{Token: (*observability.SpanExporter)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return slow, nil },
	})

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.test-slow"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			_, span := tp.Tracer("orders").Start(ctx, "place")
			span.End()
			return nil
		},
	})
	if err != nil {
		t.Errorf("a stop whose final export fits inside the bound ended the drive with %v, want nil", err)
	}
	if got := slow.count(); got != 1 {
		t.Errorf("the destination received %d span(s), want the one span ended before the stop: a final "+
			"export of %v was cut inside a bound of %v", got, bound/5, bound)
	}
}

// overrunningMetrics is a destination whose export ignores its context and
// takes the given time, and whose release answers a context that has already
// ended with that context's error, the way the upstream exporters do. The
// reader makes its final export in line, so an export that outlasts the bound
// holds the meter provider's shutdown past it, and the shutdown then returns the
// export's outcome, which the wrapper has absorbed.
type overrunningMetrics struct {
	unreachableMetrics
	takes time.Duration
}

func (m overrunningMetrics) Export(context.Context, *metricdata.ResourceMetrics) error {
	time.Sleep(m.takes)
	return nil
}

func (overrunningMetrics) Shutdown(ctx context.Context) error { return ctx.Err() }

// metricBackend is an export backend that delivers the metric capability only.
func metricBackend(name string, exporter sdkmetric.Exporter) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*observability.MetricExporter)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return exporter, nil },
	}
}

// driveOverMetricBackend runs one command with a shutdown bound of the given
// length over the one backend, records a data point while it is up, and returns
// what the drive returned and how long it took.
func driveOverMetricBackend(bound time.Duration, backend core.Module) (time.Duration, error) {
	return driveOverMetricBackends(bound, &bytes.Buffer{}, backend)
}

// driveOverMetricBackends is driveOverMetricBackend over every backend given,
// with the module's lines written to logged.
func driveOverMetricBackends(bound time.Duration, logged io.Writer, backends ...core.Module) (time.Duration, error) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(boundedConfig{bound: bound}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(logged, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	participants := []string{otel.ModuleName}
	for _, backend := range backends {
		reg.Register(backend)
		participants = append(participants, backend.Name)
	}

	start := time.Now()
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
		Action: func(ctx context.Context, reg *core.Registry) error {
			mp, err := core.Resolve[observability.MeterProvider](reg)
			if err != nil {
				return err
			}
			counter, err := mp.Meter("orders").Int64Counter("orders.placed")
			if err != nil {
				return err
			}
			counter.Add(ctx, 1)
			return nil
		},
	})
	return time.Since(start), err
}

// TestTheReleaseDoesNotReuseTheContextTheFlushExpired pins that the release of
// an exporter has a bound of its own. The final export outlasts the whole of
// shutdown-timeout, so the context the providers flushed with has expired by the
// time the exporter is released, and an exporter that answers an expired context
// with its error would fail the stop for that wait alone.
func TestTheReleaseDoesNotReuseTheContextTheFlushExpired(t *testing.T) {
	const bound = 100 * time.Millisecond
	elapsed, err := driveOverMetricBackend(bound,
		metricBackend("observability.test-overrunning", overrunningMetrics{takes: 3 * bound}))
	if err != nil {
		t.Errorf("a stop whose final export outlasted the bound ended the drive with %v, want nil: the "+
			"release must not be handed the expired context", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the stop took %v against a shutdown bound of 100ms", elapsed)
	}
}

// waitingMetrics is a destination whose export waits for its context to end,
// the way a transport retrying against a collector that is down does.
type waitingMetrics struct{ unreachableMetrics }

func (waitingMetrics) Export(ctx context.Context, _ *metricdata.ResourceMetrics) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestAStopOverTwoMetricBackendsThatWaitIsCutInsideTheBound pins the cutoff on
// the metric side of an assembly. The meter provider shuts its readers down one
// after the other on the one flush context, and a reader whose turn comes once
// that context has expired makes no final export and returns the deadline. The
// first reader's final export waits for its context, so unless the cutoff ends
// it the export uses up the bound and the second reader fails the stop.
func TestAStopOverTwoMetricBackendsThatWaitIsCutInsideTheBound(t *testing.T) {
	const bound = time.Second
	logged := &lockedBuffer{}
	elapsed, err := driveOverMetricBackends(bound, logged,
		metricBackend("observability.test-waiting-first", waitingMetrics{}),
		metricBackend("observability.test-waiting-second", waitingMetrics{}))
	if err != nil {
		t.Errorf("a stop over two metric backends that wait for their context ended the drive with %v, "+
			"want nil: each final export is cut inside the bound and its failure absorbed\n"+
			"the module logged:\n%s", err, logged)
	}
	if elapsed > bound+otel.ReleaseTimeout+releaseSlack {
		t.Errorf("the stop took %v, want at most %v", elapsed, bound+otel.ReleaseTimeout+releaseSlack)
	}
	if got := strings.Count(logged.String(), "telemetry export failed"); got != 2 {
		t.Errorf("the module logged %d export failures:\n%s\nwant one line from each backend's wrapper: "+
			"both final exports were made and cut", got, logged)
	}
}

// slowMetrics is a destination whose export honours its context and takes the
// given time to deliver, the way a reachable collector over a slow link does.
type slowMetrics struct {
	unreachableMetrics
	takes     time.Duration
	mu        *sync.Mutex
	delivered *[]string
}

func (m slowMetrics) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	t := time.NewTimer(m.takes)
	defer t.Stop()
	select {
	case <-t.C:
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, sm := range rm.ScopeMetrics {
			for _, metric := range sm.Metrics {
				*m.delivered = append(*m.delivered, metric.Name)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestAFinalMetricExportThatFitsBeforeTheCutoffIsDelivered pins where the
// cutoff falls on the metric side: the reader's final export takes a fifth of
// shutdown-timeout, against a destination that honours its context, and is
// delivered while the stop succeeds. A cutoff that ended before the meter
// provider's shutdown, or near the start of the stop, would cut it, and the
// metrics recorded since the last interval would be lost with no failure of the
// stop to show for it.
func TestAFinalMetricExportThatFitsBeforeTheCutoffIsDelivered(t *testing.T) {
	const bound = time.Second
	var (
		mu        sync.Mutex
		delivered []string
	)
	slow := slowMetrics{takes: bound / 5, mu: &mu, delivered: &delivered}

	elapsed, err := driveOverMetricBackend(bound, metricBackend("observability.test-slow-metrics", slow))
	if err != nil {
		t.Errorf("a stop whose final metric export fits inside the bound ended the drive with %v, want nil", err)
	}
	if elapsed > bound+otel.ReleaseTimeout+releaseSlack {
		t.Errorf("the stop took %v, want at most %v", elapsed, bound+otel.ReleaseTimeout+releaseSlack)
	}
	mu.Lock()
	defer mu.Unlock()
	if !contains(delivered, "orders.placed") {
		t.Errorf("the destination received metrics %v, want orders.placed: a final export of %v was cut "+
			"inside a bound of %v", delivered, bound/5, bound)
	}
}

// hangingMetrics is a destination whose release does not return until the
// context it was handed ends.
type hangingMetrics struct{ unreachableMetrics }

func (hangingMetrics) Shutdown(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestTheReleaseOfAnExporterThatWaitsForItsContextIsBounded pins that the
// release has a bound at all: the context it runs under is not the flush's, and
// the registry imposes no timeout of its own, so without one an exporter whose
// release waits for its context to end would hold the stop for good.
func TestTheReleaseOfAnExporterThatWaitsForItsContextIsBounded(t *testing.T) {
	type outcome struct {
		elapsed time.Duration
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		elapsed, err := driveOverMetricBackend(50*time.Millisecond,
			metricBackend("observability.test-hanging", hangingMetrics{}))
		done <- outcome{elapsed, err}
	}()

	// The window is in absolute terms on purpose: a ceiling built from the
	// bound's own constant would follow the constant wherever it was set.
	// The lower bound stays fixed -- a slow machine only makes the stop take
	// longer -- while the ceiling carries the caller's scale alone, because it
	// tells a release bounded to a few seconds from one bounded far longer.
	// The case's own wait is always wider than the ceiling, whatever factor
	// the run carries.
	ceiling := coretest.Declared(6 * time.Second)
	select {
	case got := <-done:
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Errorf("a release that waited for its context ended the drive with %v, want the deadline", got.err)
		}
		if got.elapsed < time.Second || got.elapsed > ceiling {
			t.Errorf("the stop took %v, want between 1s and %v: a release bound of a few seconds",
				got.elapsed, ceiling)
		}
	case <-time.After(ceiling + coretest.Scale(24*time.Second)):
		t.Fatal("the stop did not return: the release of an exporter that waits for its context is unbounded")
	}
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
