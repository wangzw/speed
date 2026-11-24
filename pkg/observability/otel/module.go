// Package otel assembles the two telemetry providers from the export
// backends a host took in, and instruments the request path.
package otel

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry, and the namespace its input
// items are mounted under.
const ModuleName = "observability.otel"

const endpointsKey = ModuleName + ".endpoints"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			// Required: this module writes log lines, and a line written through the
			// process root logger would leave configuration's destinations without
			// knowing it.
			{Token: (*log.Logger)(nil)},
			// Optional but load-bearing: the edge orders a present provider ahead of
			// this module's New, where the exporters are collected.
			{Token: (*observability.SpanExporter)(nil), Optional: true},
			{Token: (*observability.MetricExporter)(nil), Optional: true},
		},
		Provides: []core.Provision{
			// Exclusive: two assemblies of this module at once would each hand out
			// their own providers to whoever asked, so a consumer's signals would
			// split across two pipelines with nobody told.
			{Token: (*observability.TracerProvider)(nil), Exclusive: true},
			{Token: (*observability.MeterProvider)(nil), Exclusive: true},
		},
		Resources: []any{schema()},
		Prepare:   prepare,
		New:       newProviders,
		Init:      wire,
		Close:     shutdown,
	}
}

func prepare(_ context.Context, reg *core.Registry) (core.Enablement, error) {
	cfg, err := read(reg)
	if err != nil {
		return core.Enablement{}, err
	}
	if err := cfg.validate(); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func wire(_ context.Context, reg *core.Registry, instance any) error {
	p, ok := instance.(*providers)
	if !ok {
		return nil
	}
	return p.wireEndpoints(reg)
}

// wireEndpoints registers the request layer on the endpoints the configuration
// asks for: every declared endpoint for "*", exactly the listed names
// otherwise, and nothing for an empty list.
func (p *providers) wireEndpoints(reg *core.Registry) error {
	listed := p.cfg.Endpoints
	if len(listed) == 0 {
		return nil
	}
	every := len(listed) == 1 && listed[0] == allEndpoints
	router, err := core.Resolve[http.Router](reg)
	if err != nil {
		if every && errors.Is(err, core.ErrMissingProvider) {
			return nil
		}
		return routerFailure(err)
	}
	names := router.Endpoints()
	if !every {
		names = p.distinct("endpoints", listed)
	}
	for _, name := range names {
		ep, err := router.Endpoint(name)
		if err != nil && every {
			return fmt.Errorf("%s: the entry module listed the endpoint %q and then did not provide it: %w",
				ModuleName, name, err)
		}
		if err != nil {
			return fmt.Errorf("%s: %s lists the endpoint %q, and the entry module did not provide it. "+
				"Change one of two things: this list, to declared endpoints only or to none, "+
				"or the entry module's own configuration, to declare %q: %w",
				ModuleName, endpointsKey, name, name, err)
		}
		p.attach(ep, name)
	}
	return nil
}

// routerFailure names the input item behind a failed lookup of the entry module.
//
// Clearing the list is a way out of any failure here, so it is always offered;
// taking the entry module in is offered only for the failure it cures.
func routerFailure(err error) error {
	fix := "clear the list to register on no endpoint"
	if errors.Is(err, core.ErrMissingProvider) {
		fix += ", or take the entry module in on the command tree"
	}
	return fmt.Errorf("%s: %s asks for endpoints, and the entry module could not be taken up; %s: %w",
		ModuleName, endpointsKey, fix, err)
}

func (p *providers) distinct(list string, names []string) []string {
	seen := make(map[string]bool, len(names))
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			p.lg.Warn("a configured list names the same thing twice; the repeat is ignored",
				"list", list, "name", name)
			continue
		}
		seen[name] = true
		kept = append(kept, name)
	}
	return kept
}

// shutdown flushes both providers, releases them, and then releases the
// exporters they flushed through.
func shutdown(ctx context.Context, _ *core.Registry, instance any) error {
	p, ok := instance.(*providers)
	if !ok {
		return nil
	}
	flushCtx, cancel := context.WithTimeout(ctx, p.cfg.ShutdownTimeout)
	defer cancel()
	disarm := p.cut.arm(exportCutoff(p.cfg.ShutdownTimeout))
	defer disarm()

	var errs []error
	if err := p.tp.Shutdown(flushCtx); err != nil {
		errs = append(errs, err)
	}
	if err := p.mp.Shutdown(flushCtx); err != nil {
		errs = append(errs, err)
	}
	if err := releaseExporters(ctx, p.exporters); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
