// Package memory is the in-process implementation of the event-bus capability
// pkg/event declares.
//
// It delivers every event on the publisher's own goroutine, so by the time
// Publish returns every subscriber of that event has run. That is more than the
// capability promises — a bus backed by a transport cannot do it — and a caller
// must not rely on it.
//
// A handler's failure is not handed back to the publisher, here or anywhere
// else. This implementation knows each handler's result and could report it,
// but doing so would make Publish's error mean two different things: the event
// was not accepted, or the event was accepted and a subscriber failed. A
// publisher cannot act on the second the way it acts on the first, and it has
// no way to tell them apart. The failure is written to the assembly's logger
// instead.
//
// A handler that panics is recovered, reported the same way, and the handlers
// after it still run. A panic is a subscriber's failure like any other: letting
// it unwind the publisher's goroutine would hand that failure back to the
// publisher — after the publisher's own work had already been done — and
// would starve every subscriber registered after the one that panicked, which
// is exactly what the capability's floor rules out for a returned error.
//
// State is one process's memory, so a subscription made on one replica reaches
// no publisher on another. That is this implementation's deployment
// precondition, and a host declaring several replicas fails the startup with the
// replica count named, rather than getting a bus each replica publishes into
// alone.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.memory"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// replicasKey is the input item stating how many replicas share this
	// assembly's state.
	replicasKey = "replicas"
)

// Config is what this implementation accepts: the deployment fact its
// precondition is judged against.
type Config struct {
	// Replicas is how many replicas share this assembly's state.
	//
	// It states a fact about the deployment rather than a preference of this
	// implementation, and what the premise below is judged against is what the
	// configuration declares: an assembly that names nothing is one replica, and
	// a deployment of several that leaves the item at that default is read as a
	// single process here. That declared fact is all this module can judge — it
	// has no way to count the replicas that are running.
	Replicas int `config:"replicas"`
}

// init registers this module with the process registry, so a host that imports
// the package has it available.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	return event.NewModule(event.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Replicas: 1},
		Items: map[string]config.Item{
			replicasKey: {
				Description: "how many replicas share this assembly's state; this implementation " +
					"delivers within one process, so any value above 1 fails the startup",
			},
		},
		Stance: stance,
		Open:   open,
		Start:  start,
	})
}

// stance states this implementation's stance, or reports the deployment it
// cannot serve.
//
// A declared replica count above one is a deployment this backend cannot serve:
// a subscription lives in one process's memory, so a publisher on another
// replica would reach none of its subscribers, and Publish would return nil
// every time. That is a startup failure naming the item rather than a stand-down
// — this module was taken in at run level, so the host either declares one
// replica, points the capability at a backend that carries events between
// replicas, or leaves this implementation out of the command tree.
//
// Otherwise the stance is StateAuto rather than StateEnabled. This is the
// capability's zero-configuration implementation, and StateAuto is what lets a
// host import it alongside a real backend and have it stand down once that
// backend is enabled. StateDisabled is not among the answers here: it is what
// resolution gives when that stand-down happens.
func stance(cfg *Config) (core.Enablement, error) {
	if cfg.Replicas > 1 {
		return core.Enablement{}, fmt.Errorf("%s.%s is %d, and this implementation delivers within "+
			"one process: a subscriber on one replica would never see an event published on "+
			"another, while every publish still returned success. Declare one replica, point the "+
			"capability at a backend that carries events between replicas, or leave this "+
			"implementation out of the command tree",
			ConfigNamespace, replicasKey, cfg.Replicas)
	}
	return core.Enablement{State: core.StateAuto}, nil
}

// open builds the bus.
func open(_ context.Context, _ *Config, logger *slog.Logger) (event.Bus, error) {
	return &bus{table: dispatch.New("event/memory", logger)}, nil
}

// start freezes the subscription table and opens the bus to publishing.
//
// The drive calls each module's Init one at a time, so the table is not
// complete until the stage after them; freezing it there is what turns a
// subscription registered later into a defect at its call site rather than a
// subscriber that silently receives nothing published before it.
func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return fmt.Errorf("%s: the bus handed to the start stage is %T, and every constructed module "+
			"reaches this stage with the capability it delivers", ModuleName, b)
	}
	local.table.Freeze(ctx)
	return nil
}

// bus is the whole of this implementation's state: the subscription table and
// the stage it is in.
type bus struct {
	table *dispatch.Table
}

// Subscribe adds a handler for one event type. It panics once the table is
// frozen at the start stage.
func (b *bus) Subscribe(eventType string, h event.Handler) { b.table.Subscribe(eventType, h) }

// Publish delivers to every handler of this event type, on the caller's own
// goroutine.
//
// The error it returns is only ever about handing the event over: a cancelled
// context, a payload that cannot be serialized, or a bus that is not accepting
// publishes — before its start stage or from its stop stage on. A handler's
// failure is logged and left out of it — see the package documentation for why
// the two must not share one return value.
func (b *bus) Publish(ctx context.Context, evt event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The payload is checked here even though this bus hands over the
	// publisher's own value and never serializes it. The capability says a
	// payload that cannot be serialized fails the publish, and that has to hold
	// on every implementation: this is the default one, so a publisher that
	// developed against it would otherwise meet the refusal for the first time
	// on the day the assembly was pointed at a transport.
	if _, err := json.Marshal(evt.Payload); err != nil {
		return fmt.Errorf("event/memory: event %q carries a payload that cannot be serialized: %w",
			evt.Type, err)
	}
	if err := b.table.Accepting(); err != nil {
		return err
	}
	// The handlers run on the publisher's goroutine and context, with no lock
	// of this bus held: a handler that publishes again would otherwise
	// deadlock against this call. Each receives a copy of the one stamped
	// event, so they all see the same identifier.
	b.table.Deliver(ctx, dispatch.Stamp(evt))
	return nil
}

// Stop is the stop stage: a publish from here on reports ErrClosed rather than
// reaching nobody. A publish already delivering runs to its end on the
// publisher's goroutine; this bus has no readers of its own to wait for.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

// Close stops the bus if the stop stage has not. There is nothing to release.
func (b *bus) Close(_ context.Context) error { return b.table.Close() }
