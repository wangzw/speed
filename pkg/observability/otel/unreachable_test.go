package otel_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/otel"
	"github.com/vislake/speed/pkg/observability/otlp"
)

// unreachableShutdownTimeout is the bound the cases below configure. It is
// short so that a stop that waits for most of it still finishes quickly, and the
// ceiling a case allows is built from it, so a faster or slower machine moves
// neither.
const unreachableShutdownTimeout = time.Second

// releaseSlack is the time a stop may take beyond the shutdown bound and the
// release bound: scheduling, the transport's teardown, and a loaded machine.
const releaseSlack = 5 * time.Second

// stopCeiling is the longest a stop may take: the shutdown bound the cases
// configure, the bound of the release, and the slack.
const stopCeiling = unreachableShutdownTimeout + otel.ReleaseTimeout + releaseSlack

// lockedBuffer is a log destination the batch processor's goroutine and the case
// both touch.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// closedLocalAddress returns a loopback address that nothing listens on: the
// port was free a moment ago and its listener is closed again before the
// address is handed out, so a connection to it is refused.
func closedLocalAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a local port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the reserved port: %v", err)
	}
	return addr
}

// otlpConfig is a reading face that points the OTLP backend at an endpoint and
// gives this module a short shutdown bound, and leaves every other item at its
// default: the protocol is whatever the backend defaults to unless one is named.
type otlpConfig struct {
	endpoint string
	protocol string
}

func (c otlpConfig) Decode(namespace string, target any) error {
	switch namespace {
	case otel.ModuleName:
		cfg := target.(*otel.Config)
		cfg.ServiceName = "orders"
		cfg.ShutdownTimeout = unreachableShutdownTimeout
	case otlp.ModuleName:
		cfg := target.(*otlp.Config)
		cfg.Endpoint = c.endpoint
		if c.protocol != "" {
			cfg.Protocol = c.protocol
		}
	}
	return nil
}

// recordOneMetric is the action the cases here run while the assembly is up: it
// records one data point and no span, so the tracer provider has nothing queued
// at the stop and the final export is the meter provider's alone.
func recordOneMetric(ctx context.Context, reg *core.Registry) error {
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
}

// recordOneMetricAndOneSpan is the action of a run that also leaves a span in
// the batch processor's queue when the stop begins.
func recordOneMetricAndOneSpan(ctx context.Context, reg *core.Registry) error {
	if err := recordOneMetric(ctx, reg); err != nil {
		return err
	}
	tp, err := core.Resolve[observability.TracerProvider](reg)
	if err != nil {
		return err
	}
	_, span := tp.Tracer("orders").Start(ctx, "orders.place")
	span.End()
	return nil
}

// silentLocalAddress returns a loopback address whose listener accepts every
// connection and never answers on it, the way a collector that is up but not
// serving does. The listener and its connections are closed when the case ends.
func silentLocalAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on a local port: %v", err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return ln.Addr().String()
}

// stopOverAnUnreachableCollector drives the assembler and the real OTLP backend
// through the action to the end of a run in which the collector's port refuses
// connections, and reports how long it took, what the module logged, and what
// the drive returned.
func stopOverAnUnreachableCollector(t *testing.T, protocol string, action func(context.Context, *core.Registry) error) (time.Duration, string, error) {
	t.Helper()
	return stopOverCollectorAt(t, closedLocalAddress(t), protocol, action)
}

// stopOverCollectorAt is stopOverAnUnreachableCollector with the collector at
// the given endpoint.
func stopOverCollectorAt(t *testing.T, endpoint, protocol string, action func(context.Context, *core.Registry) error) (time.Duration, string, error) {
	t.Helper()
	logged := &lockedBuffer{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(otlpConfig{endpoint: endpoint, protocol: protocol}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(logged, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(otlp.Module())

	start := time.Now()
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, otlp.ModuleName},
		Action:       action,
	})
	// The batch processor's goroutine and the reader write to the buffer as well
	// as the case reading it, which is why the buffer is a synchronised one.
	return time.Since(start), logged.String(), err
}

// TestAStopOverAnUnreachableCollectorSucceeds is the case for the promise that a
// failed final export is not a failure of the stop, held against the real gRPC
// exporter, which retries a refused connection. A refused connection is
// Unavailable to it and it retries until its context ends, its own timeout
// passes or its retry budget runs out, none of which comes before a bound of a
// second, so the final export of the meter provider runs until the cutoff or the
// bound ends it and the wrapper absorbs the failure. With a single reader the
// cutoff is not what lets this stop succeed — the reader returns at the bound
// either way — so the case holds the release and the absorption, and the
// cutoff is held by TestAStopOverTwoMetricBackendsThatWaitIsCutInsideTheBound.
// The release of the exporters ends inside its own bound here whether or not
// that bound is the flush's, so the case does not tell the two apart:
// TestTheReleaseDoesNotReuseTheContextTheFlushExpired does.
func TestAStopOverAnUnreachableCollectorSucceeds(t *testing.T) {
	elapsed, logged, err := stopOverAnUnreachableCollector(t, otlp.ProtocolGRPC, recordOneMetric)
	if err != nil {
		t.Errorf("a stop over a collector that refuses connections ended the drive with %v, want nil: "+
			"a failed final export is reported by the wrapper's line, not by Close\nthe module logged:\n%s", err, logged)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the drive ended with the deadline: the release of the exporters reused the expired context")
	}
	if elapsed > stopCeiling {
		t.Errorf("the stop took %v, want at most %v: the shutdown bound plus the release bound plus slack", elapsed, stopCeiling)
	}
	if !strings.Contains(logged, "telemetry export failed") {
		t.Errorf("the module logged no export failure:\n%s\nwant the wrapper's line: the final export did try the "+
			"unreachable collector", logged)
	}
}

// TestAStopWithASpanQueuedOverAnUnreachableCollectorSucceeds holds the same stop
// with a span still queued in the batch processor, over the real gRPC exporter.
// The processor runs its final export under batch.export-timeout, far past the
// shutdown bound, and the exporter retries until that context ends or its own
// timeout of ten seconds passes, so only the cutoff ends the export inside the
// bound; without it the tracer provider's shutdown reaches the bound and fails
// the stop with the deadline. Both wrappers write their line, one for each
// signal's failed export.
func TestAStopWithASpanQueuedOverAnUnreachableCollectorSucceeds(t *testing.T) {
	elapsed, logged, err := stopOverAnUnreachableCollector(t, otlp.ProtocolGRPC, recordOneMetricAndOneSpan)
	if err != nil {
		t.Errorf("a stop with a span queued over a collector that refuses connections ended the drive with %v, "+
			"want nil: the final exports are cut short inside the bound and their failures absorbed\n"+
			"the module logged:\n%s", err, logged)
	}
	if elapsed > stopCeiling {
		t.Errorf("the stop took %v, want at most %v: the shutdown bound plus the release bound plus slack", elapsed, stopCeiling)
	}
	if got := strings.Count(logged, "telemetry export failed"); got < 2 {
		t.Errorf("the module logged %d export failures:\n%s\nwant one line from each wrapper: the span "+
			"export is cut while it retries, and the metric export, begun after the cutoff, is cut at "+
			"its start", got, logged)
	}
}

// TestAStopOverAnUnreachableCollectorOverHTTPSucceeds is the same stop over the
// other protocol: its exporter does not retry a refused connection, so the final
// export fails at once, without waiting for the cutoff, and is reported and
// absorbed like the gRPC one.
func TestAStopOverAnUnreachableCollectorOverHTTPSucceeds(t *testing.T) {
	elapsed, logged, err := stopOverAnUnreachableCollector(t, otlp.ProtocolHTTP, recordOneMetric)
	if err != nil {
		t.Errorf("a stop over a collector that refuses connections ended the drive with %v, want nil\n"+
			"the module logged:\n%s", err, logged)
	}
	if elapsed > stopCeiling {
		t.Errorf("the stop took %v, want at most %v", elapsed, stopCeiling)
	}
	if !strings.Contains(logged, "telemetry export failed") {
		t.Errorf("the module logged no export failure:\n%s\nwant the wrapper's line", logged)
	}
}

// TestAStopWithASpanQueuedOverAnUnreachableCollectorOverHTTPSucceeds holds a stop
// with a span still queued over the HTTP exporter, which gives up on a refused
// connection at once, so the same run, span included, stops cleanly.
func TestAStopWithASpanQueuedOverAnUnreachableCollectorOverHTTPSucceeds(t *testing.T) {
	elapsed, logged, err := stopOverAnUnreachableCollector(t, otlp.ProtocolHTTP, recordOneMetricAndOneSpan)
	if err != nil {
		t.Errorf("a stop with a span queued over a collector that refuses connections ended the drive with %v, want nil\n"+
			"the module logged:\n%s", err, logged)
	}
	if elapsed > stopCeiling {
		t.Errorf("the stop took %v, want at most %v", elapsed, stopCeiling)
	}
}

// TestAStopOverACollectorThatDoesNotAnswerOverHTTPIsCutInsideTheBound holds the
// HTTP exporter against a collector that accepts the connection and never
// answers. The exporter waits for the answer until its context ends, and the
// processor's context is bounded by batch.export-timeout, so the final span
// export runs until the cutoff ends it; without it the tracer provider's
// shutdown reaches the bound and fails the stop with the deadline.
func TestAStopOverACollectorThatDoesNotAnswerOverHTTPIsCutInsideTheBound(t *testing.T) {
	elapsed, logged, err := stopOverCollectorAt(t, silentLocalAddress(t), otlp.ProtocolHTTP, recordOneMetricAndOneSpan)
	if err != nil {
		t.Errorf("a stop with a span queued over a collector that does not answer ended the drive with %v, "+
			"want nil: the final exports are cut short inside the bound and their failures absorbed\n"+
			"the module logged:\n%s", err, logged)
	}
	// The exports wait for the cutoff here rather than failing at once, which
	// is what tells this case from the refused connection above.
	if elapsed < unreachableShutdownTimeout/2 {
		t.Errorf("the stop took %v, want no less than %v: the exports should have waited for the "+
			"collector until the cutoff", elapsed, unreachableShutdownTimeout/2)
	}
	if elapsed > stopCeiling {
		t.Errorf("the stop took %v, want at most %v: the shutdown bound plus the release bound plus slack", elapsed, stopCeiling)
	}
	if got := strings.Count(logged, "telemetry export failed"); got < 2 {
		t.Errorf("the module logged %d export failures:\n%s\nwant one line from each wrapper", got, logged)
	}
}
