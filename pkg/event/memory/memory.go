// Package memory is the in-process implementation of the event-bus
// capability pkg/event declares.
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
	"github.com/vislake/speed/pkg/event/internal/tracing"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.memory"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	systemName      = "memory"
	replicasKey     = "replicas"
)

// Config is what this implementation accepts: the deployment fact its
// precondition is judged against.
type Config struct {
	// Replicas is how many replicas share this assembly's state.
	Replicas int `config:"replicas"`
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	m := event.NewModule(event.Spec[Config]{
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
		Trace:  tracing.Provider,
		Open:   open,
		Start:  start,
	})
	// The optional tracer requirement is appended here rather than declared
	// by the root package, which cannot name the telemetry provider's type.
	m.Requires = append(m.Requires, tracing.Requirement())
	return m
}

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

func open(_ context.Context, _ *Config, logger *slog.Logger, tracer event.TracerProvider) (event.Bus, error) {
	return &bus{table: dispatch.New("event/memory", systemName, logger, tracer, tracing.Propagator())}, nil
}

func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return dispatch.ForeignBus(ModuleName, b)
	}
	local.table.Freeze(ctx)
	return nil
}

type bus struct {
	table *dispatch.Table
}

// Subscribe adds a handler for one event type, under the name its delivery's
// span is recorded under. It panics once the table is frozen at the start
// stage, or when the name is empty.
func (b *bus) Subscribe(eventType string, name string, h event.Handler) {
	b.table.Subscribe(eventType, name, h)
}

func (b *bus) Publish(ctx context.Context, evt event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The payload is checked here even though this bus hands over the
	// publisher's own value and never serializes it.
	if _, err := json.Marshal(evt.Payload); err != nil {
		return fmt.Errorf("event/memory: event %q carries a payload that cannot be serialized: %w",
			evt.Type, err)
	}
	if err := b.table.Accepting(); err != nil {
		return err
	}
	// The handlers run on the publisher's goroutine and context, with no lock
	// of this bus held: a handler that publishes again would otherwise
	// deadlock against this call.
	b.table.Deliver(ctx, b.table.Stamp(ctx, evt))
	return nil
}

// Stop is the stop stage: a publish from here on reports ErrClosed rather than
// reaching nobody. A publish already delivering runs to its end on the
// publisher's goroutine; this bus has no readers of its own to wait for.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

func (b *bus) Close(_ context.Context) error { return b.table.Close() }
