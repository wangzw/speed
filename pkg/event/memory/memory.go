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
// no way to tell them apart. The failure is written to the process's default
// logger instead.
//
// A handler that panics is not recovered. A panic is a defect in the handler,
// and swallowing it here would leave the publisher unable to distinguish a
// subscriber that ran from one that did not.
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
	"slices"
	"sync"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
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
func open(_ context.Context, _ *Config) (event.Bus, error) {
	return &bus{handlers: make(map[string][]event.Handler)}, nil
}

// start freezes the subscription table.
//
// The drive calls each module's Init one at a time, so the table is not
// complete until the stage after them; freezing it there is what turns a
// subscription registered later into a defect at its call site rather than a
// subscriber that silently receives nothing published before it.
func start(_ context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return fmt.Errorf("%s: the bus handed to the start stage is %T, and every constructed module "+
			"reaches this stage with the capability it delivers", ModuleName, b)
	}
	local.freeze()
	return nil
}

// bus is the whole of this implementation's state.
type bus struct {
	mu       sync.RWMutex
	handlers map[string][]event.Handler
	frozen   bool
	closed   bool
}

// Subscribe adds a handler for one event type.
//
// It panics once the table is frozen. The promise Subscribe makes is that the
// handler sees every event of that type published after the subscription, and a
// subscription arriving after the bus started publishing cannot keep it for
// anything published in between — while nothing at the call site would show
// that. Refusing at the call site is the only place the mistake is visible.
func (b *bus) Subscribe(eventType string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.frozen {
		panic(fmt.Sprintf("event/memory: Subscribe(%q) arrived after the subscription table was "+
			"frozen at the start stage. Every module subscribes in its own Init, which the drive "+
			"calls before that stage; a subscription registered later would miss everything "+
			"published before it and nothing would say so", eventType))
	}
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// Publish delivers to every handler of this event type, on the caller's own
// goroutine.
//
// The error it returns is only ever about handing the event over: a cancelled
// context, or a bus that is closed. A handler's failure is logged and left out
// of it — see the package documentation for why the two must not share one
// return value.
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
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return event.ErrClosed
	}
	// The snapshot is taken under the lock and the handlers are called without
	// it: a handler that subscribes, or one that publishes, would otherwise
	// deadlock against the lock this call is holding.
	handlers := slices.Clone(b.handlers[evt.Type])
	b.mu.RUnlock()

	for i, h := range handlers {
		if err := h(ctx, evt); err != nil {
			slog.Default().Error("event/memory: a subscriber failed; the publisher is not told",
				"event", evt.Type, "handler", i, "err", err)
		}
	}
	return nil
}

// Close stops delivery. A publish after it reports ErrClosed rather than
// silently reaching nobody.
func (b *bus) Close(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

// freeze closes the subscription table.
func (b *bus) freeze() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frozen = true
}
