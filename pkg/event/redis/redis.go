// Package redis is the Redis implementation of the event-bus capability
// pkg/event declares.
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
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/redis"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.redis"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	systemName      = "redis"
	streamPrefix    = "speed:events:"
	readCount       = 64
	// readBlock bounds how long a read waits for an entry before looping.
	readBlock = time.Second
	// newGroupStart is where a consumer group begins when it is created: at the
	// end of the stream. A group that started at the beginning would deliver
	// every event ever published to a replica starting today.
	newGroupStart = "$"
	maxEntriesKey = "max-entries"
	// defaultMaxEntries is what that item takes when the configuration names
	// nothing.
	defaultMaxEntries = 10000
	// closeWait bounds removing what this process leaves in the consumer
	// groups when it closes, so an unreachable server does not hold up the
	// process's exit once per subscribed type.
	closeWait = 5 * time.Second
)

// Config is what this implementation accepts: the connection, plus where this
// replica's consumer group sits.
type Config struct {
	redis.Config
	// ConsumerGroup names this replica's consumer group.
	ConsumerGroup string `config:"consumer-group"`
	// MaxEntries bounds how many entries a stream keeps.
	MaxEntries int64 `config:"max-entries"`
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	proto := Config{Config: redis.Defaults()}
	m := event.NewModule(event.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &proto,
		Items:           items(),
		Stance:          stance,
		Trace:           tracing.Provider,
		Open:            open,
		Start:           start,
		// The password is the credential this backend holds; it has no shape to
		// match, so the factory registers the value itself as a literal
		// redaction rule before this module reaches the server.
		Secrets: func(cfg *Config) []string { return []string{cfg.Password} },
	})
	// The optional tracer requirement is appended here rather than declared by
	// the root package, which cannot name the telemetry provider's type.
	m.Requires = append(m.Requires, tracing.Requirement())
	return m
}

func items() map[string]config.Item {
	out := redis.Items()
	out[redis.PoolSizeKey] = config.Item{
		Description: "the largest number of connections publishing keeps open; the readers take one " +
			"connection each, one per subscribed type, on top of it",
	}
	out[maxEntriesKey] = config.Item{
		Description: "how many entries a stream keeps before the oldest are trimmed, so that a bus " +
			"left running does not fill the server it shares",
	}
	out["consumer-group"] = config.Item{
		Description: "the name of this replica's consumer group; leaving it out gives this process " +
			"a group of its own, which is what makes every replica receive every event, and which " +
			"the process removes when it closes",
	}
	return out
}

// stance states this implementation's stance or reports what it cannot start
// without: an absent address fails the startup, and a configured one states
// StateEnabled.
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

func open(ctx context.Context, cfg *Config, logger *slog.Logger, tracer event.TracerProvider) (event.Bus, error) {
	client, err := redis.Connect(ctx, cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	group := cfg.ConsumerGroup
	if group == "" {
		group = defaultGroupName()
	}
	maxEntries := cfg.MaxEntries
	// A non-positive bound falls back to the default: Redis reads a negative one
	// as no limit, so a typo would silently remove the bound.
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	return &bus{
		client:     client,
		logger:     logger,
		group:      group,
		ownGroup:   cfg.ConsumerGroup == "",
		consumer:   defaultConsumerName(),
		maxEntries: maxEntries,
		table:      dispatch.New("event/redis", systemName, logger, tracer, tracing.Propagator()),
	}, nil
}

func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return dispatch.ForeignBus(ModuleName, b)
	}
	return local.begin(ctx)
}

// bus is the capability over one client for publishing, and one more for its
// readers once they start.
type bus struct {
	// client carries every publish. The readers never draw from its pool.
	client *goredis.Client
	// readers carries the readers' commands. begin builds it, and Close
	// releases it after the readers have returned.
	readers    *goredis.Client
	logger     *slog.Logger
	group      string
	ownGroup   bool
	consumer   string
	maxEntries int64
	// types is what the table held when it froze: the streams this bus holds
	// a group on. begin writes it before any reader starts, and Close reads
	// it once they have returned.
	types []string

	table *dispatch.Table
}

// Subscribe registers a handler for one event type, under the name its
// delivery's span is recorded under. It panics once the readers have started at
// the start stage, or when the name is empty.
func (b *bus) Subscribe(eventType string, name string, h event.Handler) {
	b.table.Subscribe(eventType, name, h)
}

func (b *bus) Publish(ctx context.Context, evt event.Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("%s: event %q carries a payload that cannot be serialized: %w",
			ModuleName, evt.Type, err)
	}
	if stageErr := b.table.Accepting(); stageErr != nil {
		return stageErr
	}
	evt = b.table.Stamp(ctx, evt)
	return b.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey(evt.Type),
		MaxLen: b.maxEntries,
		Approx: true,
		Values: entryValues(evt, payload),
	}).Err()
}

// Stop is the stop stage: publishing is refused, the readers take nothing more
// from their streams, and what they had already taken is delivered and
// acknowledged within the table's grace.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

func (b *bus) Close(ctx context.Context) error {
	errs := []error{b.table.Close()}
	if len(b.types) > 0 {
		b.leaveGroups(ctx)
		b.types = nil
	}
	if b.readers != nil {
		errs = append(errs, b.readers.Close())
	}
	return errors.Join(append(errs, b.client.Close())...)
}

func (b *bus) leaveGroups(ctx context.Context) {
	removing, cancel := context.WithTimeout(ctx, closeWait)
	defer cancel()
	for _, eventType := range b.types {
		var err error
		if b.ownGroup {
			err = b.client.XGroupDestroy(removing, streamKey(eventType), b.group).Err()
		} else {
			err = b.client.XGroupDelConsumer(removing, streamKey(eventType), b.group, b.consumer).Err()
		}
		if err != nil {
			b.logger.Warn("event/redis: removing what this process left in the consumer group failed",
				"event", eventType, "group", b.group, "err", err)
		}
	}
}

func (b *bus) begin(ctx context.Context) error {
	types, first := b.table.Freeze(ctx)
	if !first || len(types) == 0 {
		return nil
	}
	b.types = types
	b.readers = readerClient(b.client, len(types))
	for _, eventType := range types {
		// MkStream creates the stream when it does not exist yet, so a
		// subscriber for a type nobody has published can still be read from
		// once someone does.
		if err := b.readers.XGroupCreateMkStream(ctx, streamKey(eventType), b.group, newGroupStart).Err(); err != nil &&
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

func readerClient(publishing *goredis.Client, readers int) *goredis.Client {
	opts := *publishing.Options()
	opts.PoolSize = readers
	return goredis.NewClient(&opts)
}

func (b *bus) read(reading, handling context.Context, stream, eventType string) {
	for {
		res, err := b.readers.XReadGroup(reading, &goredis.XReadGroupArgs{
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
				// The stream or group is gone, and every read answers NOGROUP until it
				// exists again, so the reader would deliver nothing for this type.
				if createErr := b.readers.XGroupCreateMkStream(reading, stream, b.group, newGroupStart).Err(); createErr != nil &&
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
					return
				}
				b.table.Deliver(handling, decode(eventType, msg.Values))
				// Acknowledged whatever the handlers returned: the entry has been handed
				// over, and a pending entry would never be delivered again with nobody
				// left to tell.
				if err := b.readers.XAck(handling, stream, b.group, msg.ID).Err(); err != nil &&
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
	fieldTrace    = "trace"
)

func entryValues(evt event.Event, payload []byte) map[string]any {
	return map[string]any{
		fieldID:       evt.ID,
		fieldType:     evt.Type,
		fieldTenantID: evt.TenantID,
		fieldPayload:  string(payload),
		fieldTrace:    dispatch.EncodeTraceContext(evt.TraceContext),
	}
}

func decode(eventType string, values map[string]any) event.Event {
	evt := event.Event{Type: eventType}
	if v, ok := values[fieldID].(string); ok {
		evt.ID = v
	}
	if v, ok := values[fieldTenantID].(string); ok {
		evt.TenantID = v
	}
	if raw, ok := values[fieldTrace].(string); ok {
		evt.TraceContext = dispatch.DecodeTraceContext(raw)
	}
	if raw, ok := values[fieldPayload].(string); ok {
		if payload, err := dispatch.Payload([]byte(raw)); err == nil {
			evt.Payload = payload
		}
	}
	return evt
}

func streamKey(eventType string) string { return streamPrefix + eventType }

func defaultGroupName() string { return "speed-" + replica.Name() }

func defaultConsumerName() string { return "consumer-" + replica.Name() }
