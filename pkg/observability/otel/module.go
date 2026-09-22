// Package otel assembles the two telemetry providers from the export backends
// a host took in, and instruments the request path and the event bus.
//
// It is the only module of this capability that builds a provider. The export
// backends each deliver an exporter and know nothing about how one is driven;
// this module collects every exporter the registry holds, wraps each one so an
// export failure is visible, and hands the wrapped set to the processors and
// readers that push them. That split is what lets a host add a destination
// without this module knowing it exists.
package otel

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry, and the namespace its input
// items are mounted under.
const ModuleName = "observability.otel"

// init registers this module with the process registry, so a host that imports
// the package has it available.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			// Required: this module writes log lines, and a line written through
			// the process root logger would leave configuration's destinations
			// without knowing it. The logger module delivers exclusively, so a
			// command that runs this module names it too.
			{Token: (*log.Logger)(nil)},
			// Optional, and load-bearing rather than a formality: the exporters
			// are collected in New, and a dependency edge is what puts a present
			// provider ahead of this module. Marking them optional also keeps the
			// closure from pulling a backend in on its own — which one to export
			// to is the host's decision, written on the command tree.
			{Token: (*observability.SpanExporter)(nil), Optional: true},
			{Token: (*observability.MetricExporter)(nil), Optional: true},
		},
		Provides: []core.Provision{
			// Exclusive: two assemblies of this module at once would each hand
			// out their own providers to whoever asked, so a consumer's signals
			// would split across two pipelines with nobody told. The conflict is
			// named during resolution instead.
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

// prepare reads this module's own configuration and states that it starts.
//
// It reads nothing else, and in particular not another module's stance: no
// module's product exists yet at this point, and the stage promises no order.
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

// wire registers the request layer on each configured endpoint and subscribes
// to each configured event type.
//
// Neither is attempted when its list is empty, so a process with no listening
// endpoint and no event bus — a background worker — carries this module
// without requiring an entry module or a bus to be present. A name that is
// configured and cannot be taken up is a startup failure, handed back as it
// came: the sentinel for a missing provider is core's, the one for an undeclared
// endpoint is the entry module's, and neither gets a second name here.
func wire(ctx context.Context, reg *core.Registry, instance any) error {
	p, ok := instance.(*providers)
	if !ok {
		return nil
	}
	if len(p.cfg.Endpoints) > 0 {
		router, err := core.Resolve[http.Router](reg)
		if err != nil {
			return err
		}
		for _, name := range p.distinct("endpoints", p.cfg.Endpoints) {
			ep, err := router.Endpoint(name)
			if err != nil {
				return err
			}
			ep.Use(p.layer(name))
		}
	}
	if len(p.cfg.Events) > 0 {
		bus, err := core.Resolve[event.Bus](reg)
		if err != nil {
			return err
		}
		for _, evt := range p.distinct("events", p.cfg.Events) {
			bus.Subscribe(evt, p.spanFor())
		}
	}
	return nil
}

// distinct drops repeated names from one of the configured lists, keeping the
// order they were written in.
//
// A name written twice is not two registrations anyone means. The entry module
// appends a layer per call and does not dedupe, so a repeated endpoint carries
// two identical layers: every request on it opens two server spans and counts
// twice in the histogram, which reads from outside as twice the traffic. A
// repeated event type subscribes twice and produces two spans per fact.
//
// The intent of a repeated name is the single name, so that is what it gets,
// and the line reports that the configuration was not quite what it said — a
// host reviewing its own settings otherwise has no way to find out.
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

// shutdown flushes both providers and releases them.
//
// The bound comes from configuration because a Close callback receives a
// context with its cancellation stripped, and it must be bounded because the
// registry does not impose a timeout of its own. A shutdown that times out
// cuts the export short; what was in flight is then lost with no signal, which
// the module's documentation states as a cost rather than promising otherwise.
//
// Every shape a rollback can hand it is tolerated: a startup that failed
// part-way closes every module whatever stage it reached, so this runs on an
// instance whose New never returned.
func shutdown(ctx context.Context, _ *core.Registry, instance any) error {
	p, ok := instance.(*providers)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.ShutdownTimeout)
	defer cancel()

	var errs []error
	if err := p.tp.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := p.mp.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
