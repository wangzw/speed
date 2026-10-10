package otel_test

import (
	"bytes"
	"context"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/otel"
)

// ratioConfig gives this module a service name and a sample ratio.
type ratioConfig struct{ ratio float64 }

func (c ratioConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		cfg := target.(*otel.Config)
		cfg.ServiceName = "orders"
		cfg.SampleRatio = c.ratio
	}
	return nil
}

// sampledAt runs one assembly at the given ratio and starts three spans the way
// the request layer does: one continuing a caller that sampled its trace, one
// continuing a caller that did not, and one with no caller at all. It returns
// the names of the spans that were recorded and exported, and the module's log.
func sampledAt(t *testing.T, ratio float64) ([]string, string) {
	t.Helper()
	d := &destination{}
	var module bytes.Buffer
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(ratioConfig{ratio: ratio}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(&module, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(backendModule("observability.test-backend", d))
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.test-backend"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			tracer := tp.Tracer("orders")
			for name, flags := range map[string]string{"sampled-caller": "01", "unsampled-caller": "00"} {
				header := nethttp.Header{}
				header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-"+flags)
				parented := observability.Propagator().Extract(ctx, propagation.HeaderCarrier(header))
				_, span := tracer.Start(parented, name)
				span.End()
			}
			_, span := tracer.Start(ctx, "no-caller")
			span.End()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	names := slices.Clone(d.spans)
	slices.Sort(names)
	return names, module.String()
}

// TestTheRatioDecidesOnlyTheTracesThisProcessStarts pins what sample-ratio
// governs. The sampler follows a parent's decision, so that a trace is not
// broken apart by each process sampling it on its own: a request whose caller
// sampled its trace is recorded even at zero, one whose caller did not is not
// recorded even at one, and the ratio decides for a request with no caller. The
// line written at zero has to say that rather than claim nothing is recorded.
func TestTheRatioDecidesOnlyTheTracesThisProcessStarts(t *testing.T) {
	atZero, logged := sampledAt(t, 0)
	if want := []string{"sampled-caller"}; !slices.Equal(atZero, want) {
		t.Errorf("at ratio 0 the spans %v were recorded, want %v", atZero, want)
	}
	if strings.Contains(logged, "no trace is recorded") ||
		!strings.Contains(logged, "a request whose caller sampled its trace is still recorded") {
		t.Errorf("at ratio 0 the module logged:\n%s\nwant a line saying that no trace starts here and "+
			"that a caller's sampled trace is still recorded", logged)
	}

	atOne, _ := sampledAt(t, 1)
	if want := []string{"no-caller", "sampled-caller"}; !slices.Equal(atOne, want) {
		t.Errorf("at ratio 1 the spans %v were recorded, want %v", atOne, want)
	}
}
