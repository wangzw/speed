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
	"fmt"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry, and the namespace its input
// items are mounted under.
const ModuleName = "observability.otel"

// The input items whose lists make wire take another module up, spelled as the
// error text names them.
const (
	endpointsKey = ModuleName + ".endpoints"
	eventsKey    = ModuleName + ".events"
)

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
			// command that runs this module names it too, or the host supplies an
			// instance for it.
			{Token: (*log.Logger)(nil)},
			// Optional, and load-bearing rather than a formality: the exporters
			// are collected in New, and a dependency edge is what puts a present
			// provider ahead of this module. Marking them optional also keeps the
			// closure from pulling a backend in on its own — which one to export
			// to is the host's decision, written on the command tree or made by
			// supplying an instance for the backend.
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
// module's New has run at this point, so the registry holds only the instances
// the host supplied, the configuration reader among them, and the stage
// promises no order.
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
// The entry module is taken up only when the endpoint list is not empty and
// the bus only when the event list is not empty, so a process with no listening
// endpoint and no event bus — a background worker — carries this module
// without either being present. The endpoint list defaults to "*", every
// endpoint the entry module declares, and for that one value a missing entry
// module is not a failure: nothing in the configuration named an endpoint, so
// nothing asked for one. Only the missing-provider sentinel is read that way.
// Any other failure of the lookup, such as two router providers that neither
// declare exclusivity and that the registry cannot choose between, is handed
// back, because skipping it would leave a process serving requests with no layer
// on any of them.
//
// A list of names asks for those endpoints and nothing else, so an entry module
// that is absent, or a name it never declared, is a startup failure. So is a
// bus that is absent for a configured event type. Each such failure wraps the
// sentinel it came with, which stays matchable by errors.Is, and names the
// input item that asked, so the reader of the line is told which setting to
// clear or which module to take in.
//
// It acts on this module's own product only. An instance the host supplied for
// the module is not one this module built, and it is left alone: no layer is
// registered and no event type is subscribed for it, and whether it produces
// any signal is the supplier's concern.
func wire(ctx context.Context, reg *core.Registry, instance any) error {
	p, ok := instance.(*providers)
	if !ok {
		return nil
	}
	if err := p.wireEndpoints(reg); err != nil {
		return err
	}
	if len(p.cfg.Events) > 0 {
		bus, err := core.Resolve[event.Bus](reg)
		if err != nil {
			return busFailure(err)
		}
		for _, evt := range p.distinct("events", p.cfg.Events) {
			bus.Subscribe(evt, p.spanFor())
		}
	}
	return nil
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
		ep.Use(p.layer(name))
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

// busFailure names the input item behind a failed lookup of the bus, with the
// same two ways out routerFailure offers.
func busFailure(err error) error {
	fix := "clear the list to subscribe to no event type"
	if errors.Is(err, core.ErrMissingProvider) {
		fix += ", or take the event bus in on the command tree"
	}
	return fmt.Errorf("%s: %s names event types, and the event bus could not be taken up; %s: %w",
		ModuleName, eventsKey, fix, err)
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

// shutdown flushes both providers, releases them, and then releases the
// exporters they flushed through.
//
// The exporters come last because a backend's product serves both providers
// with one Shutdown: releasing it between the two would leave the second
// provider's final export with nowhere to go. See releaseExporters.
//
// The bound comes from configuration because a Close callback receives a
// context with its cancellation stripped, and it must be bounded because the
// registry does not impose a timeout of its own. A tracer provider's shutdown
// that reaches the bound stops waiting and returns the deadline, so the drive
// fails and the host sees that the stop did not complete; what was still in
// flight is lost, and which records those were is not reported — the module's
// documentation states that as a cost rather than promising otherwise. The
// meter provider's shutdown does not stop at the bound while a reader exports:
// the reader makes its final export in line and returns once that export has.
// A reader whose turn comes after the bound has gone, used up by the tracer
// provider or by a reader before it, makes no final export and returns the
// deadline, which fails the stop as well.
//
// The exports are held to the bound as well. At exportCutoff of the bound the
// cutoff ends, and every export the wrappers forward that is still running then
// or begins after, the one in flight when Close began and the final ones alike,
// is cut short; the wrapper reports and absorbs the failure, and the provider
// has the rest of the bound to return in. Without it the batch processor's
// final export, which runs under batch.export-timeout rather than under this
// bound, would outlast the bound whenever it retries against an unreachable
// collector, and the tracer provider's shutdown would fail with the deadline. The tracer provider's
// shutdown can still reach the bound for a reason the cutoff does not touch,
// such as a span exporter that ignores its context, and that fails the stop. A
// metric exporter that ignores its context holds the meter provider's shutdown
// until its export returns; the stop then succeeds only if no reader comes
// after it.
//
// The bound covers the two providers and their final exports, and not the
// release of the exporters: a provider's shutdown may have used up the bound,
// and the release that follows would find the context already expired and fail
// for that alone. The release is bounded on its own.
//
// Every shape the drive can hand it is tolerated. A startup that failed
// part-way closes every module whatever stage it reached, so this runs on an
// instance whose New never returned. An instance the host supplied for this
// module is not this module's product either, and it is left alone: what it
// holds is released by whoever supplied it.
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
