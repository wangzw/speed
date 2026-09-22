package stdout

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// newExporters builds the two exporters from this module's configuration.
//
// A construction failure is handed back wrapped rather than given a name of its
// own: the judgement is the upstream's and the fix is the writer the
// configuration gave, so a sentinel here would name a category this module
// cannot describe any better than the error text already does.
func newExporters(_ context.Context, reg *core.Registry) (any, error) {
	cfg, err := read(reg)
	if err != nil {
		return nil, err
	}
	var spanOpts []stdouttrace.Option
	var metricOpts []stdoutmetric.Option
	if cfg.Pretty {
		spanOpts = append(spanOpts, stdouttrace.WithPrettyPrint())
		metricOpts = append(metricOpts, stdoutmetric.WithPrettyPrint())
	}
	span, err := stdouttrace.New(spanOpts...)
	if err != nil {
		return nil, fmt.Errorf("%s: the span exporter could not be built: %w", ModuleName, err)
	}
	metric, err := stdoutmetric.New(metricOpts...)
	if err != nil {
		// The span exporter was built and nothing holds it: the failure returns
		// from New, so no provider exists to shut it down later.
		_ = span.Shutdown(context.Background())
		return nil, fmt.Errorf("%s: the metric exporter could not be built: %w", ModuleName, err)
	}
	return &observability.Exporters{Span: span, Metric: metric}, nil
}
