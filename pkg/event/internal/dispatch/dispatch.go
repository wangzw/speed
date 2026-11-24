// Package dispatch is the part of an event bus every implementation shares: the
// subscription table and its freeze, the start stage's refusal, the acceptance
// stage, and the isolated running of subscribers.
package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
)

// Grace bounds how long the stop stage waits for the subscribers already
// running when it begins.
var Grace = 10 * time.Second

const settle = time.Second

type stage int

const (
	collecting stage = iota
	accepting
	// stopped is everything after the stop stage began.
	stopped
)

// Table is one bus's subscriptions and its stage.
type Table struct {
	// name prefixes what this table writes: a log record, a panic, an error.
	name   string
	logger *slog.Logger
	grace  time.Duration

	system string
	tracer event.TracerProvider
	prop   propagation.TextMapPropagator

	mu       sync.Mutex
	handlers map[string][]subscription
	stage    stage

	reading    context.Context
	endReading context.CancelFunc
	// handling is what a transport's subscribers run on. It does not end at
	// the stop stage: a subscriber already running finishes on a context that
	// is still live, and only the grace running out cancels it.
	handling    context.Context
	endHandling context.CancelFunc

	readers  sync.WaitGroup
	goneOnce sync.Once
	gone     chan struct{}

	stopOnce sync.Once
	stopErr  error
}

type subscription struct {
	name    string
	handler event.Handler
}

// New builds an empty table.
func New(name, system string, logger *slog.Logger, tracer event.TracerProvider, prop propagation.TextMapPropagator) *Table {
	return &Table{
		name:     name,
		logger:   logger,
		grace:    Grace,
		system:   system,
		tracer:   tracer,
		prop:     prop,
		handlers: make(map[string][]subscription),
	}
}

// Subscribe registers h for events whose type is eventType, under the name the
// span of every delivery to it is recorded.
func (t *Table) Subscribe(eventType string, name string, h event.Handler) {
	if name == "" {
		panic(fmt.Sprintf("%s: Subscribe(%q) was given no subscriber name. The name is what the span "+
			"of every delivery to this subscriber is recorded under, and it is required: the "+
			"subscriber already holds its own identity — its module name, or a component within it",
			t.name, eventType))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stage != collecting {
		panic(fmt.Sprintf("%s: Subscribe(%q) arrived after the subscription table was frozen at the "+
			"start stage. Every module subscribes in its own Init, which the drive calls before that "+
			"stage; a subscription registered later would miss everything published before it and "+
			"nothing would say so", t.name, eventType))
	}
	t.handlers[eventType] = append(t.handlers[eventType], subscription{name: name, handler: h})
}

// Freeze closes the subscription table and opens the bus to publishing.
func (t *Table) Freeze(ctx context.Context) ([]string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stage != collecting {
		return nil, false
	}
	t.stage = accepting
	base := context.WithoutCancel(ctx)
	t.reading, t.endReading = context.WithCancel(base)
	t.handling, t.endHandling = context.WithCancel(base)
	return slices.Sorted(maps.Keys(t.handlers)), true
}

// Accepting reports whether a publish is accepted now, and the error to refuse
// it with when it is not: ErrNotStarted before the start stage, ErrClosed from
// the stop stage on.
func (t *Table) Accepting() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.stage {
	case collecting:
		return fmt.Errorf("%s: %w: a publish is accepted from the start stage on, once every module's "+
			"Init has subscribed", t.name, event.ErrNotStarted)
	case stopped:
		return fmt.Errorf("%s: %w", t.name, event.ErrClosed)
	default:
		return nil
	}
}

// ForeignBus is the error an implementation's start stage returns for a
// bus that is not the product its own Open built.
func ForeignBus(module string, b event.Bus) error {
	return fmt.Errorf("%s: the bus handed to the start stage is %T, which this implementation did not "+
		"open: the stage takes up its own product only, so a bus supplied for the module cannot be "+
		"started here. Supply a bus built by the New of module %q, or supply none for it; a test that "+
		"needs a stand-in for the event capability registers a placeholder module that declares only "+
		"event.Bus and supplies the stand-in to that one", module, b, module)
}

// Stamp gives evt the identifier Publish assigns when the publisher left
// it empty, and leaves a non-empty one as it is.
func Stamp(evt event.Event) event.Event {
	if evt.ID == "" {
		evt.ID = event.NewID()
	}
	return evt
}

// Stamp gives the event the identifier Publish assigns and the tracing
// context of the span ctx is in, and returns it. A non-empty identifier
// and a non-empty tracing context are each carried as they are.
func (t *Table) Stamp(ctx context.Context, evt event.Event) event.Event {
	evt = Stamp(evt)
	evt.TraceContext = StampTraceContext(evt.TraceContext, ctx, t.prop)
	return evt
}

// StampTraceContext is the tracing context to record for an event: what
// the event already carries, or the span ctx is in when it carries none.
func StampTraceContext(carried map[string]string, ctx context.Context, prop propagation.TextMapPropagator) map[string]string {
	if len(carried) > 0 {
		return carried
	}
	carrier := propagation.MapCarrier{}
	prop.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return map[string]string(carrier)
}

// EncodeTraceContext renders a tracing carrier as the single text value a
// backend whose entry values are strings — a Redis stream field, a
// table column — holds it in.
func EncodeTraceContext(carrier map[string]string) string {
	if len(carrier) == 0 {
		return ""
	}
	raw, err := json.Marshal(carrier)
	if err != nil {
		return ""
	}
	return string(raw)
}

// DecodeTraceContext reads a carrier back from the text form
// EncodeTraceContext writes.
func DecodeTraceContext(text string) map[string]string {
	if text == "" {
		return nil
	}
	var carrier map[string]string
	if err := json.Unmarshal([]byte(text), &carrier); err != nil {
		return nil
	}
	if len(carrier) == 0 {
		return nil
	}
	return carrier
}

// Payload turns the serialized payload a transport carried back into the shape a
// subscriber reads, with every number kept as a json.Number.
func Payload(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var payload any
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the payload carries data after its value")
	}
	return payload, nil
}

const (
	attrDestinationName = "messaging.destination.name"
	attrMessageID       = "messaging.message.id"
	attrOperation       = "messaging.operation"
	attrSystem          = "messaging.system"
	operationProcess    = "process"
)

// Deliver runs every handler subscribed to evt's type, in subscription order,
// on ctx.
func (t *Table) Deliver(ctx context.Context, evt event.Event) {
	t.mu.Lock()
	handlers := slices.Clone(t.handlers[evt.Type])
	t.mu.Unlock()
	for i, sub := range handlers {
		spanCtx, span := t.startSpan(ctx, sub.name, evt)
		err := run(spanCtx, sub.handler, evt)
		span.End(err)
		if err != nil {
			t.logger.Error(t.name+": a subscriber failed; the publisher is not told",
				"event", evt.Type, "handler", i, "name", sub.name, "err", err)
		}
	}
}

func (t *Table) startSpan(ctx context.Context, name string, evt event.Event) (context.Context, event.Span) {
	parent := trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	if len(evt.TraceContext) > 0 {
		parent = t.prop.Extract(parent, propagation.MapCarrier(evt.TraceContext))
	}
	return t.tracer.Start(parent, name, map[string]string{
		attrDestinationName: evt.Type,
		attrMessageID:       evt.ID,
		attrOperation:       operationProcess,
		attrSystem:          t.system,
	})
}

func run(ctx context.Context, h event.Handler, evt event.Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the handler panicked: %v", r)
		}
	}()
	return h(ctx, evt)
}

// Go runs one reader of a transport-backed bus on a goroutine of its own.
func (t *Table) Go(read func(reading, handling context.Context)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stage != accepting {
		return
	}
	reading, handling := t.reading, t.handling
	t.readers.Add(1)
	go func() {
		defer t.readers.Done()
		read(reading, handling)
	}()
}

// Stop is the bus's stop stage: publishing is refused from here on, the
// readers take nothing more, and the subscribers already running are
// given Grace to finish on a context that is still live.
func (t *Table) Stop() error {
	t.stopOnce.Do(func() { t.stopErr = t.stop() })
	return t.stopErr
}

func (t *Table) stop() error {
	t.mu.Lock()
	t.stage = stopped
	endReading, endHandling := t.endReading, t.endHandling
	t.mu.Unlock()
	if endReading == nil {
		return nil
	}
	endReading()
	if t.readersGone(t.grace) {
		return nil
	}
	endHandling()
	t.readersGone(settle)
	return fmt.Errorf("%s: subscribers were still running %s after the stop stage began; their context "+
		"has been cancelled, and whatever this replica had taken from its backend and not yet handed "+
		"over is left undelivered", t.name, t.grace)
}

// Close runs the stop stage if it has not run, and then ends the subscribers'
// context whatever the stop stage found. It returns what the stop stage
// reported, so a Close that was the first to stop the bus does not lose it.
func (t *Table) Close() error {
	err := t.Stop()
	t.mu.Lock()
	endHandling := t.endHandling
	t.mu.Unlock()
	if endHandling != nil {
		endHandling()
	}
	return err
}

func (t *Table) readersGone(d time.Duration) bool {
	t.goneOnce.Do(func() {
		t.gone = make(chan struct{})
		go func() {
			t.readers.Wait()
			close(t.gone)
		}()
	})
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.gone:
		return true
	case <-timer.C:
		return false
	}
}
