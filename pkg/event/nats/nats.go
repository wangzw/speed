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
	// consumerRetryWait is how long a reader waits before opening its durable
	// again after a failed open. It is shorter than fetchWait because it sits
	// on the startup path and on the way back from a durable found gone, where
	// the first retries are the ones that usually succeed.
	consumerRetryWait = 250 * time.Millisecond
	// inactiveThreshold is how long the server keeps a durable named after
	// its process once no one has pulled from it. A process that closes
	// removes its own durables; this is what removes those of a process that
	// never reached its close. A running reader pulls at least every
	// fetchWait, so only a durable nobody reads is idle this long, and one
	// removed under a reader cut off from the server for longer is opened
	// again when the reader reaches it.
	inactiveThreshold = 24 * time.Hour
)

// Config is what this implementation accepts.
type Config struct {
	// URL is the server to connect to. This implementation is pointed at a
	// backend by it, and a deployment that takes the implementation in without
	// setting it fails the startup rather than reaching the first publish.
	URL string `config:"url"`
	// ConsumerName names this instance's durable consumer. It defaults to a
	// name unique to this process, which is what makes the delivery a
	// broadcast: consumers sharing a name split the stream between them. A
	// durable of the process's own is removed when the process closes, or by
	// the server once it has been idle for inactiveThreshold; a configured one
	// stays.
	ConsumerName string `config:"consumer-name"`
	// Timeout bounds connecting, and every request to the server made on a
	// context with no deadline of its own. One that is not positive takes the
	// default.
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
					"this process a durable of its own, which is what makes every replica receive " +
					"every event, and which is removed when the process closes or has been gone a day; a " +
					"name holds no dot, wildcard, path separator or whitespace",
			},
			timeoutKey: {
				Description: "how long connecting, and each request to the server that carries no " +
					"deadline of its own, may take; written with a unit",
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
	// JetStream refuses such a durable name only when the durable is created,
	// at the start stage, with an error that names a stream rather than the
	// input. Judged here, it fails the startup naming the item, as every other
	// premise does.
	if strings.ContainsAny(cfg.ConsumerName, durableNameForbidden) {
		return core.Enablement{}, fmt.Errorf("%s.%s is %q, and a JetStream durable name may not hold "+
			"a dot, a wildcard, a path separator or whitespace", ConfigNamespace, consumerNameKey, cfg.ConsumerName)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// durableNameForbidden is what JetStream refuses in a durable consumer's name.
const durableNameForbidden = ".*> /\\\t\r\n"

// open connects.
func open(ctx context.Context, cfg *Config, logger *slog.Logger) (event.Bus, error) {
	// A timeout that is not positive is a configuration gone wrong: the
	// library reads zero on the connect as no bound at all, and refuses it for
	// a request. Folding it back onto the default keeps a typo from removing
	// the bound.
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	nc, err := nats.Connect(cfg.URL,
		nats.Timeout(timeout),
		// A bus lives as long as its process. The library's default gives up
		// reconnecting after two minutes, and past that the connection is
		// closed for good: every publish and every fetch would fail until the
		// process restarted, whenever the server came back.
		nats.MaxReconnects(-1),
		// The client's buffer of outgoing data during a reconnect is off, so a
		// publish it cannot reach the server with fails at once. Held in that
		// buffer, the publish would be sent once the connection came back —
		// after the caller had been told the bus refused it, which is the one
		// thing design-event's lower bound rules out. The library spells "off"
		// as -1; zero is read as "unset" and puts its eight-megabyte default
		// back.
		nats.ReconnectBufSize(-1),
		// Losing the server and getting it back are the only signals of a bus
		// that cannot deliver, so both reach the assembly's log. A clean close
		// reports no error and says nothing.
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Error("event/nats: the connection to the server was lost; reconnecting", "err", err)
			}
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			logger.Info("event/nats: reconnected to the server", "server", conn.ConnectedUrlRedacted())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("%s: connecting: %w", ModuleName, err)
	}
	// The timeout bounds every request made on a context with no deadline of
	// its own — a publish on a request's context, the stream and consumer
	// management — and not only the connect.
	js, err := jetstream.New(nc, jetstream.WithDefaultTimeout(timeout))
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
		nc:          nc,
		js:          js,
		logger:      logger,
		consumer:    name,
		ownConsumer: cfg.ConsumerName == "",
		timeout:     timeout,
		maxEntries:  maxEntries,
		namespace:   namespace,
		table:       dispatch.New("event/nats", logger),
		streams:     make(map[string]bool),
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
	nc       *nats.Conn
	js       jetstream.JetStream
	logger   *slog.Logger
	consumer string
	// ownConsumer reports that the durable's name is this process's own
	// rather than one the host configured: no other process reads it, so it
	// is removed when this one closes and left to the server's inactivity
	// threshold when it never does.
	ownConsumer bool
	// timeout bounds removing this process's durables when it closes.
	timeout    time.Duration
	maxEntries int64
	// namespace is the first token of every subject this bus touches, and the
	// prefix of every stream it names.
	namespace string

	// table holds the subscriptions, the stage, and the readers' lifetime.
	table *dispatch.Table
	// types is what the table held when it froze: the streams this bus holds a
	// durable on. begin writes it before any reader starts, and Close reads it
	// once they have returned.
	types []string

	mu sync.Mutex
	// streams records the types whose stream is known to be there. It is what
	// keeps a publish from re-issuing the stream definition every time: the
	// library's upsert rewrites the whole configuration, so a host that had
	// capped a stream by hand would have that cap reset on the next publish,
	// with nothing said.
	streams map[string]bool
}

// forgetStream drops what this bus knows about a type's stream, so the next
// ensureStream creates it again. It is for a stream found gone — deleted, or
// lost with a server's storage.
func (b *bus) forgetStream(eventType string) {
	b.mu.Lock()
	delete(b.streams, eventType)
	b.mu.Unlock()
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
// A non-nil return means the event was not stored. The connection holds no
// queue of outgoing data across a reconnect, so a publish issued while the
// server is unreachable fails then and there rather than being sent once the
// connection is back. The one window this leaves is a request already on its
// way when the link broke: its acknowledgement can be lost on the way back,
// and the server may have stored the event although the call returned an
// error.
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
	if errors.Is(err, jetstream.ErrNoStreamResponse) {
		// No stream took the message: the one this bus created is gone —
		// deleted, or lost with the server's storage — and without creating it
		// again every publish of this type would fail until the process
		// restarted. Nothing stored the message, so publishing it again cannot
		// store it twice.
		b.forgetStream(evt.Type)
		if streamErr := b.ensureStream(ctx, evt.Type); streamErr != nil {
			return streamErr
		}
		_, err = b.js.Publish(ctx, b.subject(evt.Type), body)
	}
	return err
}

// Stop is the stop stage: publishing is refused, the readers fetch nothing
// more, and what their last fetch brought is delivered and acknowledged within
// the table's grace.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

// Close stops the bus if the stop stage has not, removes the durables named
// after this process, and releases the connection. Closing the connection
// flushes what it still buffers, the acknowledgements of the last entries
// delivered included.
//
// A durable named after this process is read by no other, so one left behind
// would never be read again, and every process start would add one per
// subscribed type. A durable the host named is shared, or resumes across a
// restart, and stays. Failing to remove one does not fail the close: the
// server removes it once it has been idle for inactiveThreshold.
func (b *bus) Close(ctx context.Context) error {
	stopErr := b.table.Close()
	if b.ownConsumer && len(b.types) > 0 {
		removing, cancel := context.WithTimeout(ctx, b.timeout)
		for _, eventType := range b.types {
			if err := b.js.DeleteConsumer(removing, b.streamName(eventType), b.consumer); err != nil &&
				!errors.Is(err, jetstream.ErrConsumerNotFound) && !errors.Is(err, jetstream.ErrStreamNotFound) {
				b.logger.Warn("event/nats: removing this process's consumer failed; the server removes it "+
					"once it has been idle for "+inactiveThreshold.String(), "event", eventType, "err", err)
			}
		}
		cancel()
		b.types = nil
	}
	b.nc.Close()
	return stopErr
}

// begin freezes the table and starts one reader per subscribed type.
func (b *bus) begin(ctx context.Context) error {
	types, first := b.table.Freeze(ctx)
	if !first {
		return nil
	}
	b.types = types
	for _, eventType := range types {
		if _, err := b.openConsumer(ctx, eventType); err != nil {
			return err
		}
	}
	for _, eventType := range types {
		b.table.Go(func(reading, handling context.Context) {
			b.read(reading, handling, eventType)
		})
	}
	return nil
}

// consumerConfig is the durable this bus reads each stream through.
func (b *bus) consumerConfig() jetstream.ConsumerConfig {
	cfg := jetstream.ConsumerConfig{
		Durable:   b.consumer,
		AckPolicy: jetstream.AckExplicitPolicy,
		// DeliverNew keeps a replica that starts today from being handed
		// every event ever published, which is the "no history for a late
		// subscriber" behaviour the capability states.
		DeliverPolicy: jetstream.DeliverNewPolicy,
	}
	if b.ownConsumer {
		cfg.InactiveThreshold = inactiveThreshold
	}
	return cfg
}

// openConsumer makes sure the type's stream and this bus's durable on it are
// there, and hands the durable back.
//
// It is idempotent, which is what lets a reader call it again whenever its
// durable is found gone. A stream that is gone as well is created again
// first; the durable, created anew, starts at the stream's end.
func (b *bus) openConsumer(ctx context.Context, eventType string) (jetstream.Consumer, error) {
	if err := b.ensureStream(ctx, eventType); err != nil {
		return nil, err
	}
	consumer, err := b.js.CreateOrUpdateConsumer(ctx, b.streamName(eventType), b.consumerConfig())
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		b.forgetStream(eventType)
		if streamErr := b.ensureStream(ctx, eventType); streamErr != nil {
			return nil, streamErr
		}
		consumer, err = b.js.CreateOrUpdateConsumer(ctx, b.streamName(eventType), b.consumerConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("%s: opening the consumer for %q: %w", ModuleName, eventType, err)
	}
	return consumer, nil
}

// read delivers what its stream hands this instance, until the stop stage.
//
// A fetch is not cut short by the stop stage: it runs to the end of its wait,
// and whatever it brought is delivered and acknowledged. An entry this
// consumer was handed and never acknowledged goes back to the durable only
// after the server's acknowledgement wait, and only a consumer of the same
// name ever receives it again.
func (b *bus) read(reading, handling context.Context, eventType string) {
	for {
		consumer := b.take(reading, eventType)
		if consumer == nil {
			return
		}
		if !b.fetch(reading, handling, consumer, eventType) {
			return
		}
	}
}

// take opens this bus's durable on the type's stream, retrying until it is
// open or the stop stage begins, when it returns nil.
//
// A failure here is retried rather than given up on: giving up would leave
// this event type undelivered for the rest of the process's life while a
// single log line said so, and Publish went on returning nil.
func (b *bus) take(reading context.Context, eventType string) jetstream.Consumer {
	for {
		if reading.Err() != nil {
			return nil
		}
		consumer, err := b.openConsumer(reading, eventType)
		if err == nil {
			return consumer
		}
		if reading.Err() != nil {
			// The failure is the stop stage cancelling the call, not the
			// server, and an error line for it would misdirect whoever reads
			// the log.
			return nil
		}
		b.logger.Error("event/nats: opening the consumer failed; retrying", "event", eventType, "err", err)
		if !pause(reading, consumerRetryWait) {
			return nil
		}
	}
}

// fetch delivers what the durable hands this bus. It returns false when the
// stop stage has begun, and true when the durable is gone, so the reader opens
// it again.
//
// Every other failure — of the fetch, or of the batch it brought — pauses for
// fetchWait before the next one. A failure that repeats at once, as every
// fetch on a vanished durable does, would otherwise spin against the server
// and write an error line a few times a millisecond.
func (b *bus) fetch(reading, handling context.Context, consumer jetstream.Consumer, eventType string) bool {
	for {
		if reading.Err() != nil {
			return false
		}
		batch, err := consumer.Fetch(batchSize, jetstream.FetchMaxWait(fetchWait))
		if err == nil {
			if !b.deliver(handling, batch, eventType) {
				return false
			}
			err = batch.Error()
		}
		if err == nil || reading.Err() != nil {
			continue
		}
		if consumerGone(err) {
			b.logger.Error("event/nats: the consumer is gone; opening it again", "event", eventType, "err", err)
			return true
		}
		b.logger.Error("event/nats: fetching failed; retrying", "event", eventType, "err", err)
		if !pause(reading, fetchWait) {
			return false
		}
	}
}

// deliver hands every entry of a batch to the subscribers and acknowledges
// it. It returns false when the stop stage's grace ran out part-way; what is
// left of the batch stays unacknowledged.
func (b *bus) deliver(handling context.Context, batch jetstream.MessageBatch, eventType string) bool {
	for msg := range batch.Messages() {
		if handling.Err() != nil {
			return false
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
	return true
}

// consumerGone reports whether a fetch failed because the durable it pulls
// from is no longer on the server: deleted while the pull was waiting, or
// already gone, which leaves the pull with no one to answer it.
func consumerGone(err error) bool {
	return errors.Is(err, jetstream.ErrConsumerDeleted) ||
		errors.Is(err, jetstream.ErrConsumerNotFound) ||
		errors.Is(err, nats.ErrNoResponders)
}

// pause waits d, and reports false when the stop stage began first.
func pause(reading context.Context, d time.Duration) bool {
	select {
	case <-reading.Done():
		return false
	case <-time.After(d):
		return true
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
