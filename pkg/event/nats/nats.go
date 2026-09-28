// Package nats is the NATS JetStream implementation of the event-bus
// capability pkg/event declares.
//
// Every event type gets a stream of its own, and every bus instance takes a
// durable pull consumer of its own on each of them. JetStream balances a stream
// between consumers that share a name; a name unique to the instance is what
// makes this a broadcast — two instances of this bus in two processes both
// receive every event rather than splitting them.
//
// Delivery to this instance's subscribers goes through the same path as
// delivery to any other's: the consumer. A publish here does not hand the event
// to a local handler before returning, which is the property the capability's
// floor is written around.
//
// An entry is acknowledged once it has been handed to the local handlers,
// whatever they returned. A failure has nowhere to go — no publisher is waiting
// on a read that happens after its own call returned — and leaving an entry
// unacknowledged would have it redelivered forever, which is a worse answer
// than reporting it to the assembly's log once.
package nats

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/replica"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.nats"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// namespaceKey is the input item that separates one assembly from another.
	namespaceKey = "namespace"
	// defaultNamespace is what that item takes when the configuration names
	// nothing. It keeps a single assembly working out of the box; two
	// assemblies on one server have to be told apart by hand, because nothing
	// in this module can tell them apart for the host.
	defaultNamespace = "speed_events"
	// batchSize bounds how many entries one fetch takes.
	batchSize = 64
	// fetchWait bounds how long a fetch waits before looping.
	fetchWait = 2 * time.Second
	// consumerRetryWait is how long a reader waits before taking the consumer
	// again after a failed take. It is shorter than fetchWait because it sits
	// on the startup path: the consumer was created moments earlier, so the
	// first retries are the ones that usually succeed.
	consumerRetryWait = 250 * time.Millisecond
)

// Config is what this implementation accepts.
type Config struct {
	// URL is the server to connect to. This implementation is pointed at a
	// backend by it, and a deployment that takes the implementation in without
	// setting it fails the startup rather than reaching the first publish.
	URL string `config:"url"`
	// ConsumerName names this instance's durable consumer. It defaults to a
	// name unique to this process, which is what makes the delivery a
	// broadcast: consumers sharing a name split the stream between them.
	ConsumerName string `config:"consumer-name"`
	// Timeout bounds connecting and one operation.
	Timeout time.Duration `config:"timeout"`
	// MaxEntries bounds how many entries each stream keeps. A stream with no
	// bound grows with every event ever published, on a server whose storage it
	// shares with everything else on it.
	MaxEntries int64 `config:"max-entries"`
	// Namespace separates this assembly's streams from every other's on the
	// same server.
	//
	// It has to be set wherever two assemblies share a server, and nothing else
	// can do it for them: a stream's name is derived from the event type alone,
	// so two assemblies publishing to one type would share a stream and each
	// replica's consumer would receive the other's events — tenant identifiers
	// included. A server carrying one assembly needs nothing here.
	Namespace string `config:"namespace"`
}

const (
	urlKey          = "url"
	consumerNameKey = "consumer-name"
	timeoutKey      = "timeout"
)

const defaultTimeout = 5 * time.Second

// maxEntriesKey is the input item bounding a stream's length.
const maxEntriesKey = "max-entries"

// defaultMaxEntries is what that item takes when the configuration names
// nothing. A default rather than no bound, because no bound is the failure this
// exists to prevent.
const defaultMaxEntries = 10000

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	return event.NewModule(event.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Timeout: defaultTimeout},
		Items: map[string]config.Item{
			urlKey: {
				Description: "the NATS server to connect to; this implementation has to be pointed " +
					"at one, so leaving it out fails the startup when the implementation is taken in",
			},
			consumerNameKey: {
				Description: "the name of this replica's durable consumer; leaving it out gives " +
					"this process a name of its own, which is what makes every replica receive " +
					"every event",
			},
			timeoutKey: {
				Description: "how long connecting and one operation may take, written with a unit",
			},
			maxEntriesKey: {
				Description: "how many entries each stream keeps before the oldest are discarded, so " +
					"that a bus left running does not fill the server it shares",
			},
			namespaceKey: {
				Description: "the prefix this assembly's streams and subjects carry; two assemblies " +
					"sharing one server must each be given one of their own, or they will each " +
					"receive the other's events",
			},
		},
		Stance: stance,
		Open:   open,
		Start:  start,
	})
}

// stance states this implementation's stance, or reports what it cannot start
// without.
//
// A module taken in at run level has to start, so an absent server is a startup
// failure naming the item rather than a stand-down, and a configured one states
// StateEnabled, which is what makes a second configured backend fail the
// startup.
func stance(cfg *Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no server, and this implementation cannot "+
			"start without one: it was taken in at run level and delivers the event capability, "+
			"which it reaches over NATS. Set it, or leave this implementation out of the command "+
			"tree and let another backend deliver the capability",
			ConfigNamespace, urlKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// open connects.
func open(ctx context.Context, cfg *Config, logger *slog.Logger) (event.Bus, error) {
	nc, err := nats.Connect(cfg.URL, nats.Timeout(cfg.Timeout))
	if err != nil {
		return nil, fmt.Errorf("%s: connecting: %w", ModuleName, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("%s: opening JetStream: %w", ModuleName, err)
	}
	name := cfg.ConsumerName
	if name == "" {
		name = defaultConsumerName()
	}
	// A negative bound is a configuration gone wrong, and JetStream reads it as
	// "no limit"; folding it back onto the default keeps a typo from silently
	// removing the bound.
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	if err := checkNamespace(namespace); err != nil {
		nc.Close()
		return nil, err
	}
	return &bus{
		nc:         nc,
		js:         js,
		logger:     logger,
		consumer:   name,
		maxEntries: maxEntries,
		namespace:  namespace,
		table:      dispatch.New("event/nats", logger),
		streams:    make(map[string]bool),
	}, nil
}

// start freezes the table, opens the bus to publishing, and begins fetching.
func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return fmt.Errorf("%s: the bus handed to the start stage is %T", ModuleName, b)
	}
	return local.begin(ctx)
}

type bus struct {
	nc         *nats.Conn
	js         jetstream.JetStream
	logger     *slog.Logger
	consumer   string
	maxEntries int64
	// namespace is the first token of every subject this bus touches, and the
	// prefix of every stream it names.
	namespace string

	// table holds the subscriptions, the stage, and the readers' lifetime.
	table *dispatch.Table

	mu sync.Mutex
	// streams records the types whose stream is known to be there. It is what
	// keeps a publish from re-issuing the stream definition every time: the
	// library's upsert rewrites the whole configuration, so a host that had
	// capped a stream by hand would have that cap reset on the next publish,
	// with nothing said.
	streams map[string]bool
}

// ensureStream makes sure the type's stream exists, and does it once.
//
// A second instance racing to create the same stream is not a failure: it lost
// a race it did not need to win, and the stream it wanted is there.
func (b *bus) ensureStream(ctx context.Context, eventType string) error {
	b.mu.Lock()
	known := b.streams[eventType]
	b.mu.Unlock()
	if known {
		return nil
	}
	_, err := b.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     b.streamName(eventType),
		Subjects: []string{b.subject(eventType)},
		MaxMsgs:  b.maxEntries,
	})
	if err != nil && !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return fmt.Errorf("%s: opening the stream for %q: %w", ModuleName, eventType, err)
	}
	b.mu.Lock()
	b.streams[eventType] = true
	b.mu.Unlock()
	return nil
}

// Subscribe registers a handler for one event type. It panics once the readers
// have started at the start stage: a subscription registered later would miss
// everything already delivered.
func (b *bus) Subscribe(eventType string, h event.Handler) { b.table.Subscribe(eventType, h) }

// Publish writes the event to its stream.
//
// The stream is created on demand, because JetStream has no equivalent of the
// automatic creation a Redis stream gets from its first append: without this a
// publish for a type nobody had subscribed to yet would fail rather than being
// available to whoever subscribes next.
func (b *bus) Publish(ctx context.Context, evt event.Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("%s: event %q carries a payload that cannot be serialized: %w",
			ModuleName, evt.Type, err)
	}
	if stageErr := b.table.Accepting(); stageErr != nil {
		return stageErr
	}
	if streamErr := b.ensureStream(ctx, evt.Type); streamErr != nil {
		return streamErr
	}
	evt = dispatch.Stamp(evt)
	body, err := json.Marshal(envelope{
		ID: evt.ID, Type: evt.Type, TenantID: evt.TenantID, Payload: json.RawMessage(payload),
	})
	if err != nil {
		return fmt.Errorf("%s: encoding event %q: %w", ModuleName, evt.Type, err)
	}
	_, err = b.js.Publish(ctx, b.subject(evt.Type), body)
	return err
}

// Stop is the stop stage: publishing is refused, the readers fetch nothing
// more, and what their last fetch brought is delivered and acknowledged within
// the table's grace.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

// Close stops the bus if the stop stage has not, and releases the connection.
// Closing the connection flushes what it still buffers, the acknowledgements
// of the last entries delivered included.
func (b *bus) Close(_ context.Context) error {
	stopErr := b.table.Close()
	b.nc.Close()
	return stopErr
}

// begin freezes the table and starts one reader per subscribed type.
func (b *bus) begin(ctx context.Context) error {
	types, first := b.table.Freeze(ctx)
	if !first {
		return nil
	}
	for _, eventType := range types {
		stream := b.streamName(eventType)
		if err := b.ensureStream(ctx, eventType); err != nil {
			return err
		}
		if _, err := b.js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
			Durable:   b.consumer,
			AckPolicy: jetstream.AckExplicitPolicy,
			// DeliverNew keeps a replica that starts today from being handed
			// every event ever published, which is the "no history for a late
			// subscriber" behaviour the capability states.
			DeliverPolicy: jetstream.DeliverNewPolicy,
		}); err != nil {
			return fmt.Errorf("%s: opening the consumer for %q: %w", ModuleName, eventType, err)
		}
	}
	for _, eventType := range types {
		b.table.Go(func(reading, handling context.Context) {
			b.read(reading, handling, b.streamName(eventType), eventType)
		})
	}
	return nil
}

// read delivers what its stream hands this instance, until the stop stage.
//
// A fetch is not cut short by the stop stage: it runs to the end of its wait,
// and whatever it brought is delivered and acknowledged. An entry this
// consumer was handed and never acknowledged goes back to the durable only
// after the server's acknowledgement wait, and only a consumer of the same
// name ever receives it again.
func (b *bus) read(reading, handling context.Context, stream, eventType string) {
	// Taking the consumer is retried like everything else in this loop: the
	// consumer was created moments ago, so a failure here is a transient one,
	// and giving up would leave this event type undelivered for the rest of
	// the process's life while a single log line says so.
	var consumer jetstream.Consumer
	for {
		if reading.Err() != nil {
			return
		}
		taken, err := b.js.Consumer(reading, stream, b.consumer)
		if err == nil {
			consumer = taken
			break
		}
		if reading.Err() != nil {
			// The failure is the stop stage cancelling the call, not the
			// server, and an error line for it would misdirect whoever reads
			// the log.
			return
		}
		b.logger.Error("event/nats: taking the consumer failed; retrying", "event", eventType, "err", err)
		select {
		case <-reading.Done():
			return
		case <-time.After(consumerRetryWait):
		}
	}
	for {
		if reading.Err() != nil {
			return
		}
		batch, err := consumer.Fetch(batchSize, jetstream.FetchMaxWait(fetchWait))
		if err != nil {
			if reading.Err() != nil {
				return
			}
			b.logger.Error("event/nats: fetching failed; retrying", "event", eventType, "err", err)
			select {
			case <-reading.Done():
				return
			case <-time.After(fetchWait):
			}
			continue
		}
		for msg := range batch.Messages() {
			if handling.Err() != nil {
				// The stop stage's grace ran out. What is left of this fetch
				// stays unacknowledged.
				return
			}
			b.table.Deliver(handling, decode(eventType, msg.Data()))
			// Acknowledged whatever the handlers returned: the entry has been
			// handed over, and an unacknowledged one would be redelivered
			// forever with nobody left to tell.
			if err := msg.Ack(); err != nil && handling.Err() == nil {
				b.logger.Error("event/nats: acknowledging a delivered entry failed",
					"event", eventType, "err", err)
			}
		}
		if err := batch.Error(); err != nil && reading.Err() == nil {
			b.logger.Error("event/nats: the fetch ended in an error", "event", eventType, "err", err)
		}
	}
}

// envelope is what travels on the wire.
//
// The identifier, the type and the tenant ride beside the payload rather than
// inside it, so a subscriber never has to guess at field spellings to learn
// which event it holds or which tenant it belongs to.
type envelope struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	TenantID string          `json:"tenant"`
	Payload  json.RawMessage `json:"payload"`
}

// decode turns a message back into an event. The payload's numbers keep their
// decimal text; see dispatch.Payload.
func decode(eventType string, data []byte) event.Event {
	evt := event.Event{Type: eventType}
	var env envelope
	if err := json.Unmarshal(data, &env); err == nil {
		evt.ID = env.ID
		evt.TenantID = env.TenantID
		if len(env.Payload) > 0 {
			if payload, err := dispatch.Payload(env.Payload); err == nil {
				evt.Payload = payload
			}
		}
	}
	return evt
}

// streamName is the stream one event type travels on, within this bus's
// namespace.
//
// JetStream stream names admit a limited character set, so the type's bytes are
// hex-encoded: injective, so no two types share a stream.
func (b *bus) streamName(eventType string) string {
	return b.namespace + "_" + hex.EncodeToString([]byte(eventType))
}

// subject is what a stream is subscribed to. Subjects are more permissive than
// stream names but treat the dot as a separator, so the type's bytes are
// hex-encoded and joined to the prefix with an underscore rather than a dot:
// a type name may be empty — the capability puts no constraint on one — and an
// encoding joined by a dot would leave the subject ending in a separator, which
// the server refuses.
func (b *bus) subject(eventType string) string {
	return b.namespace + ".e_" + hex.EncodeToString([]byte(eventType))
}

// checkNamespace refuses a namespace a subject cannot carry.
//
// A subject is a series of dot-separated tokens and the namespace becomes the
// first of them, so a dot inside it would put every stream into a subject space
// the host did not name — and a wildcard would make this assembly's
// subscription reach into someone else's events, which is the failure the
// namespace exists to prevent.
func checkNamespace(namespace string) error {
	if strings.ContainsAny(namespace, ". \t\r\n*>") {
		return fmt.Errorf("%s: %s is %q, and a subject token may not hold a dot, whitespace or a "+
			"wildcard", ModuleName, namespaceKey, namespace)
	}
	return nil
}

// defaultConsumerName gives this process a consumer of its own, which is what
// makes the delivery a broadcast rather than a load balance.
func defaultConsumerName() string {
	// A durable name admits no dot, so the host is folded into one token.
	return strings.ReplaceAll(replica.Name(), ".", "_")
}
