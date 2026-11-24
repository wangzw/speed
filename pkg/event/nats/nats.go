// Package nats is the NATS JetStream implementation of the event-bus
// capability pkg/event declares.
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
	"github.com/vislake/speed/pkg/event/internal/tracing"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.nats"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	systemName      = "nats"
	namespaceKey    = "namespace"
	// defaultNamespace is what that item takes when the configuration names
	// nothing.
	defaultNamespace = "speed_events"
	batchSize        = 64
	// fetchWait bounds how long a fetch waits before looping.
	fetchWait = 2 * time.Second
	// consumerRetryWait is how long a reader waits before opening its durable
	// again after a failed open.
	consumerRetryWait = 250 * time.Millisecond
	// inactiveThreshold is how long the server keeps a durable named after
	// its process once no one has pulled from it.
	inactiveThreshold = 24 * time.Hour
)

// Config is what this implementation accepts.
type Config struct {
	// URL is the server to connect to.
	URL string `config:"url"`
	// ConsumerName names this instance's durable consumer. It defaults to a
	// name unique to this process, which is what makes the delivery a
	// broadcast: consumers sharing a name split the stream between them.
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
	Namespace string `config:"namespace"`
}

const (
	urlKey          = "url"
	consumerNameKey = "consumer-name"
	timeoutKey      = "timeout"
)

const defaultTimeout = 5 * time.Second

const maxEntriesKey = "max-entries"

// defaultMaxEntries is what that item takes when the configuration names
// nothing. A default rather than no bound, because no bound is the failure this
// exists to prevent.
const defaultMaxEntries = 10000

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	m := event.NewModule(event.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Timeout: defaultTimeout},
		Items: map[string]config.Item{
			urlKey: {
				// A NATS URL carries the credentials to connect with — a user
				// and password or a token in its authority — so it must not
				// reach logs, diagnostics or the help output.
				Sensitive: true,
				Description: "the NATS server to connect to, with any credentials in the URL; " +
					"this implementation has to be pointed at one, so leaving it out fails the " +
					"startup when the implementation is taken in",
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
		Trace:  tracing.Provider,
		Open:   open,
		Start:  start,
		// The URL carries the credentials to connect with in its authority, and
		// there is no shape to match them by, so the whole value is registered as
		// a literal redaction rule before this module reaches the server.
		Secrets: func(cfg *Config) []string { return []string{cfg.URL} },
	})
	// The optional tracer requirement is appended here rather than declared by
	// the root package, which cannot name the telemetry provider's type.
	m.Requires = append(m.Requires, tracing.Requirement())
	return m
}

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
	// input.
	if strings.ContainsAny(cfg.ConsumerName, durableNameForbidden) {
		return core.Enablement{}, fmt.Errorf("%s.%s is %q, and a JetStream durable name may not hold "+
			"a dot, a wildcard, a path separator or whitespace", ConfigNamespace, consumerNameKey, cfg.ConsumerName)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// durableNameForbidden is what JetStream refuses in a durable consumer's name.
const durableNameForbidden = ".*> /\\\t\r\n"

func open(ctx context.Context, cfg *Config, logger *slog.Logger, tracer event.TracerProvider) (event.Bus, error) {
	timeout := cfg.Timeout
	// A non-positive timeout falls back to the default: the library reads zero as
	// no bound on connect and refuses it for a request.
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	nc, err := nats.Connect(cfg.URL,
		nats.Timeout(timeout),
		// A bus lives as long as its process.
		nats.MaxReconnects(-1),
		// The client's buffer of outgoing data during a reconnect is off, so a
		// publish it cannot reach the server with fails at once.
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
	maxEntries := cfg.MaxEntries
	// A non-positive bound falls back to the default: JetStream reads a negative
	// one as no limit, so a typo would silently remove the bound.
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
		table:       dispatch.New("event/nats", systemName, logger, tracer, tracing.Propagator()),
		streams:     make(map[string]bool),
	}, nil
}

func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return dispatch.ForeignBus(ModuleName, b)
	}
	return local.begin(ctx)
}

type bus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	logger   *slog.Logger
	consumer string
	// ownConsumer reports whether the durable's name is this process's own, so it
	// is removed at close rather than left to the server's inactivity threshold.
	ownConsumer bool
	timeout     time.Duration
	maxEntries  int64
	// namespace is the first token of every subject this bus touches, and the
	// prefix of every stream it names.
	namespace string

	table *dispatch.Table
	// types is what the table held when it froze: the streams this bus holds a
	// durable on. begin writes it before any reader starts, and Close reads it
	// once they have returned.
	types []string

	mu sync.Mutex
	// streams records the types whose stream is known to be there.
	streams map[string]bool
}

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
	if streamErr := b.ensureStream(ctx, evt.Type); streamErr != nil {
		return streamErr
	}
	evt = b.table.Stamp(ctx, evt)
	body, err := json.Marshal(envelopeFor(evt, payload))
	if err != nil {
		return fmt.Errorf("%s: encoding event %q: %w", ModuleName, evt.Type, err)
	}
	_, err = b.js.Publish(ctx, b.subject(evt.Type), body)
	if errors.Is(err, jetstream.ErrNoStreamResponse) {
		// No stream took the message: it is gone, and without creating it again every
		// publish of this type would fail until the process restarted.
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

func (b *bus) consumerConfig() jetstream.ConsumerConfig {
	cfg := jetstream.ConsumerConfig{
		Durable:       b.consumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	}
	if b.ownConsumer {
		cfg.InactiveThreshold = inactiveThreshold
	}
	return cfg
}

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

func pause(reading context.Context, d time.Duration) bool {
	select {
	case <-reading.Done():
		return false
	case <-time.After(d):
		return true
	}
}

type envelope struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	TenantID string          `json:"tenant"`
	Payload  json.RawMessage `json:"payload"`
	// TraceContext is the event's tracing context, its keys the upstream
	// propagation format's.
	TraceContext map[string]string `json:"trace"`
}

func envelopeFor(evt event.Event, payload []byte) envelope {
	return envelope{
		ID: evt.ID, Type: evt.Type, TenantID: evt.TenantID,
		TraceContext: evt.TraceContext, Payload: payload,
	}
}

func decode(eventType string, data []byte) event.Event {
	evt := event.Event{Type: eventType}
	var env envelope
	if err := json.Unmarshal(data, &env); err == nil {
		evt.ID = env.ID
		evt.TenantID = env.TenantID
		evt.TraceContext = env.TraceContext
		if len(env.Payload) > 0 {
			if payload, err := dispatch.Payload(env.Payload); err == nil {
				evt.Payload = payload
			}
		}
	}
	return evt
}

func (b *bus) streamName(eventType string) string {
	return b.namespace + "_" + hex.EncodeToString([]byte(eventType))
}

func (b *bus) subject(eventType string) string {
	return b.namespace + ".e_" + hex.EncodeToString([]byte(eventType))
}

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
	// A durable name admits no dot, so the host's name is folded into one token.
	return strings.ReplaceAll(replica.Name(), ".", "_")
}
