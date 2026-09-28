// Package redis is the Redis implementation of the event-bus capability
// pkg/event declares.
//
// It carries events over Redis Streams. Every event type gets its own stream,
// and every bus instance reads that stream through a consumer group of its own,
// so a publish reaches each replica rather than being load-balanced onto one of
// them: two instances of this bus in two processes both see every event.
//
// Delivery to this instance's subscribers goes through the same path as
// delivery to any other's — the stream — which is why a publish here does not
// hand the event to a local handler before returning. That property is what the
// capability's floor is written around: the publisher learns whether the event
// reached the stream, and nothing about what any subscriber did with it.
//
// An entry is acknowledged as soon as it has been handed to the local handlers,
// whatever they returned. A failure has nowhere to go — no publisher is waiting
// on a read that happens after its own call returned — and leaving the entry
// pending would have it redelivered forever, which is a worse answer than
// reporting it to the assembly's log once.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/replica"
	"github.com/vislake/speed/pkg/internal/redis"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.redis"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// streamPrefix namespaces the streams this implementation creates, so an
	// assembly sharing a Redis server with anything else does not read or
	// overwrite keys that are not its own.
	streamPrefix = "speed:events:"
	// readCount bounds how many entries one read takes.
	readCount = 64
	// readBlock bounds how long a read waits for an entry before looping.
	readBlock = time.Second
	// newGroupStart is where a consumer group begins when it is created: at the
	// end of the stream. A group that started at the beginning would deliver
	// every event ever published to a replica starting today.
	newGroupStart = "$"
	// maxEntriesKey is the input item bounding a stream's length.
	maxEntriesKey = "max-entries"
	// defaultMaxEntries is what that item takes when the configuration names
	// nothing. A default rather than no bound, because no bound is the failure
	// this exists to prevent and a host that never thought about it is exactly
	// the host that needs one.
	defaultMaxEntries = 10000
)

// Config is what this implementation accepts: the connection, plus where this
// replica's consumer group sits.
type Config struct {
	redis.Config
	// ConsumerGroup names this replica's consumer group. It defaults to a name
	// unique to this process, which is what makes the delivery a broadcast:
	// Redis balances a stream between consumers of one group, so two replicas
	// sharing a group name would split the events between them instead of both
	// seeing them.
	ConsumerGroup string `config:"consumer-group"`
	// MaxEntries bounds how many entries a stream keeps. A stream with no bound
	// grows with every event ever published, on a server that is very often
	// running with a memory limit and evicting under pressure — which, for a
	// stream nobody has configured, is a failure that arrives as data loss
	// somewhere else on the same server. Trimming is approximate, which is what
	// lets Redis do it without an exact count on every append.
	MaxEntries int64 `config:"max-entries"`
}

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	proto := Config{Config: redis.Defaults()}
	return event.NewModule(event.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &proto,
		Items:           items(),
		Stance:          stance,
		Open:            open,
		Start:           start,
	})
}

// items is the shared connection declaration plus this implementation's own
// item.
func items() map[string]config.Item {
	out := redis.Items()
	out[maxEntriesKey] = config.Item{
		Description: "how many entries a stream keeps before the oldest are trimmed, so that a bus " +
			"left running does not fill the server it shares",
	}
	out["consumer-group"] = config.Item{
		Description: "the name of this replica's consumer group; leaving it out gives this process " +
			"a name of its own, which is what makes every replica receive every event",
	}
	return out
}

// stance states this implementation's stance, or reports what it cannot start
// without, on the same terms as the key-value module's: a module taken in at
// run level has to start, so an absent address is a startup failure naming the
// item rather than a stand-down, and a configured one states StateEnabled, which
// is what makes a second configured backend fail the startup.
//
// There is no third answer here. "The host did not point this implementation at
// a server, so this run does not want it" is expressed by leaving the
// implementation out of the command tree, not by the module standing itself
// down.
func stance(cfg *Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.Addr) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no server, and this implementation cannot "+
			"start without one: it was taken in at run level and delivers the event capability, "+
			"which it reaches over Redis. Set it, or leave this implementation out of the command "+
			"tree and let another backend deliver the capability",
			ConfigNamespace, redis.AddrKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// open builds the bus.
func open(ctx context.Context, cfg *Config, logger *slog.Logger) (event.Bus, error) {
	client, err := redis.Connect(ctx, cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	group := cfg.ConsumerGroup
	if group == "" {
		group = defaultGroupName()
	}
	maxEntries := cfg.MaxEntries
	// A negative bound is a configuration gone wrong, and Redis reads it as "no
	// limit"; folding it back onto the default keeps a typo from silently
	// removing the bound.
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	return &bus{
		client:     client,
		logger:     logger,
		group:      group,
		consumer:   defaultConsumerName(),
		maxEntries: maxEntries,
		table:      dispatch.New("event/redis", logger),
	}, nil
}

// start freezes the subscription table, opens the bus to publishing, and
// begins reading.
//
// The readers start here rather than in Subscribe because the table is not
// complete until every module has had its Init: a reader started on the first
// subscription would begin delivering to a table still being filled.
func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return fmt.Errorf("%s: the bus handed to the start stage is %T", ModuleName, b)
	}
	return local.begin(ctx)
}

// bus is the capability over one client.
type bus struct {
	client     *goredis.Client
	logger     *slog.Logger
	group      string
	consumer   string
	maxEntries int64

	// table holds the subscriptions, the stage, and the readers' lifetime.
	table *dispatch.Table
}

// Subscribe registers a handler for one event type. It panics once the
// readers have started at the start stage: a subscription registered later
// would miss everything already delivered.
func (b *bus) Subscribe(eventType string, h event.Handler) { b.table.Subscribe(eventType, h) }

// Publish appends the event to its stream, which every replica's group reads.
//
// The returned error is about the append alone: the event was not accepted, or
// it was. What any subscriber does with it afterwards happens on this instance's
// reader goroutines or another replica's, after this call has returned.
func (b *bus) Publish(ctx context.Context, evt event.Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("%s: event %q carries a payload that cannot be serialized: %w",
			ModuleName, evt.Type, err)
	}
	if stageErr := b.table.Accepting(); stageErr != nil {
		return stageErr
	}
	evt = dispatch.Stamp(evt)
	return b.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey(evt.Type),
		MaxLen: b.maxEntries,
		Approx: true,
		Values: map[string]any{
			fieldID:       evt.ID,
			fieldType:     evt.Type,
			fieldTenantID: evt.TenantID,
			fieldPayload:  string(payload),
		},
	}).Err()
}

// Stop is the stop stage: publishing is refused, the readers take nothing more
// from their streams, and what they had already taken is delivered and
// acknowledged within the table's grace.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

// Close stops the bus if the stop stage has not, and releases the client.
func (b *bus) Close(_ context.Context) error {
	stopErr := b.table.Close()
	return errors.Join(stopErr, b.client.Close())
}

// begin freezes the table and starts one reader per subscribed type.
func (b *bus) begin(ctx context.Context) error {
	types, first := b.table.Freeze(ctx)
	if !first {
		return nil
	}
	for _, eventType := range types {
		// MkStream creates the stream when it does not exist yet, so a
		// subscriber for a type nobody has published can still be read from
		// once someone does.
		if err := b.client.XGroupCreateMkStream(ctx, streamKey(eventType), b.group, newGroupStart).Err(); err != nil &&
			!strings.Contains(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("%s: creating the consumer group for %q: %w", ModuleName, eventType, err)
		}
	}
	for _, eventType := range types {
		b.table.Go(func(reading, handling context.Context) {
			b.read(reading, handling, streamKey(eventType), eventType)
		})
	}
	return nil
}

// read delivers what its stream hands this instance, until the stop stage.
//
// A read that returns entries is delivered in full even when the stop stage
// began while it was blocked: the entries are this consumer's from the moment
// the read returned them, and nothing in this implementation claims an entry
// back from a consumer, so one left unacknowledged here would be delivered by
// no one.
func (b *bus) read(reading, handling context.Context, stream, eventType string) {
	for {
		res, err := b.client.XReadGroup(reading, &goredis.XReadGroupArgs{
			Group:    b.group,
			Consumer: b.consumer,
			Streams:  []string{stream, ">"},
			Count:    readCount,
			Block:    readBlock,
		}).Result()
		if err != nil {
			if reading.Err() != nil {
				return
			}
			if errors.Is(err, goredis.Nil) {
				continue
			}
			if strings.Contains(err.Error(), "NOGROUP") {
				// The stream or the group is gone — destroyed by an operator,
				// flushed, or evicted under memory pressure — and every read
				// will answer NOGROUP until it exists again, so the reader
				// would deliver nothing for this type for the rest of the
				// process's life. The create is idempotent (it answers
				// BUSYGROUP when the group is already there), which is what
				// makes rerunning it here safe.
				if createErr := b.client.XGroupCreateMkStream(reading, stream, b.group, newGroupStart).Err(); createErr != nil &&
					!strings.Contains(createErr.Error(), "BUSYGROUP") && reading.Err() == nil {
					b.logger.Error("event/redis: recreating the consumer group failed",
						"event", eventType, "err", createErr)
				}
			}
			b.logger.Error("event/redis: reading the stream failed; retrying",
				"event", eventType, "err", err)
			select {
			case <-reading.Done():
				return
			case <-time.After(readBlock):
			}
			continue
		}
		for _, s := range res {
			for _, msg := range s.Messages {
				if handling.Err() != nil {
					// The stop stage's grace ran out. What is left of this
					// read stays pending in this replica's group, delivered
					// by no one.
					return
				}
				b.table.Deliver(handling, decode(eventType, msg.Values))
				// Acknowledged whatever the handlers returned: the entry has
				// been handed over, and a pending entry would never be
				// delivered again with nobody left to tell. The context is the
				// handlers', which the stop stage leaves live, so an entry
				// delivered while the bus is stopping is acknowledged too.
				if err := b.client.XAck(handling, stream, b.group, msg.ID).Err(); err != nil &&
					handling.Err() == nil {
					b.logger.Error("event/redis: acknowledging a delivered entry failed",
						"event", eventType, "id", msg.ID, "err", err)
				}
			}
		}
	}
}

// The entry field names, in one place because the writer and the reader have to
// agree on them and nothing else checks that they do.
const (
	fieldID       = "id"
	fieldType     = "type"
	fieldTenantID = "tenant"
	fieldPayload  = "payload"
)

// decode turns a stream entry back into an event.
//
// The payload is decoded into the ordinary shape rather than into whatever the
// publisher held: this path has only what was written, and a subscriber reads a
// payload structurally for exactly this reason.
func decode(eventType string, values map[string]any) event.Event {
	evt := event.Event{Type: eventType}
	if v, ok := values[fieldID].(string); ok {
		evt.ID = v
	}
	if v, ok := values[fieldTenantID].(string); ok {
		evt.TenantID = v
	}
	if raw, ok := values[fieldPayload].(string); ok {
		var payload any
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			evt.Payload = payload
		}
	}
	return evt
}

// streamKey is the stream one event type travels on.
func streamKey(eventType string) string { return streamPrefix + eventType }

// defaultGroupName gives this process a group of its own, which is what makes
// the delivery a broadcast rather than a load balance.
func defaultGroupName() string { return "speed-" + replica.Name() }

// defaultConsumerName names this process within its group.
func defaultConsumerName() string { return "consumer-" + replica.Name() }
