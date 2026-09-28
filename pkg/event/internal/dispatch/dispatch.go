// Package dispatch is the part of an event bus every implementation of
// pkg/event shares: the subscription table and its freeze, the stage that
// decides whether a publish is accepted, running a subscriber isolated from
// the others, and — for an implementation that reads from a transport — the
// lifetime of its readers and the bounded wait for the subscribers still
// running when the bus stops.
//
// The implementations differ in how an event travels. They must not differ in
// any of this, since each of these is a promise the capability makes whatever
// backend carries it, so it is written once here.
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

	"github.com/vislake/speed/pkg/event"
)

// Grace bounds how long the stop stage waits for the subscribers already
// running when it begins. A subscriber still running then is handed a
// cancelled context, which is what lets one that honours its context return;
// settle is how much longer the stage waits for it to do so.
const (
	Grace  = 10 * time.Second
	settle = time.Second
)

// stage is where a bus is in its lifecycle, as far as publishing and
// subscribing are concerned.
type stage int

const (
	// collecting runs from construction until the start stage: modules
	// subscribe, and nothing is published, since the table is not complete.
	collecting stage = iota
	// accepting runs from the start stage until the stop stage.
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

	mu       sync.Mutex
	handlers map[string][]event.Handler
	stage    stage

	// reading is what a reader reads the backend under. The stop stage
	// cancels it, and that is what tells every reader to take nothing more.
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

// New builds an empty table. name is the prefix of everything the table
// writes, and logger is where it writes a subscriber's failure.
func New(name string, logger *slog.Logger) *Table {
	return &Table{
		name:     name,
		logger:   logger,
		grace:    Grace,
		handlers: make(map[string][]event.Handler),
	}
}

// Subscribe registers h for events whose type is eventType.
//
// It panics once the table is frozen. The promise a subscription makes is that
// the handler sees every event of its type published after it, and one
// arriving after the bus opened to publishing cannot keep that promise for
// anything published in between — while nothing at the call site would show
// it. Refusing at the call site is the only place the mistake is visible.
func (t *Table) Subscribe(eventType string, h event.Handler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stage != collecting {
		panic(fmt.Sprintf("%s: Subscribe(%q) arrived after the subscription table was frozen at the "+
			"start stage. Every module subscribes in its own Init, which the drive calls before that "+
			"stage; a subscription registered later would miss everything published before it and "+
			"nothing would say so", t.name, eventType))
	}
	t.handlers[eventType] = append(t.handlers[eventType], h)
}

// Freeze closes the subscription table and opens the bus to publishing. It
// returns the subscribed types, sorted, and whether this call was the one that
// froze the table: a second call, or one after the stop stage began, finds
// nothing to do.
//
// ctx is the start stage's. The contexts the bus reads and handles under are
// derived from it with its values but without its cancellation: the drive's
// context is cancelled the moment a host begins shutting down, which is before
// any module's stop stage, and delivery is to continue until this bus's own.
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

// Stamp gives evt the identifier Publish assigns when the publisher left it
// empty, and leaves a non-empty one as it is.
//
// Every implementation stamps before the event leaves the publisher's call —
// before it is written to a backend, and before the in-process bus hands it
// to its first subscriber — so the one value is what every subscriber on
// every replica receives, and a redelivery of what the backend holds carries
// it again.
func Stamp(evt event.Event) event.Event {
	if evt.ID == "" {
		evt.ID = event.NewID()
	}
	return evt
}

// Payload turns the serialized payload a transport carried back into the shape
// a subscriber reads: the ordinary decoded structure, with every number kept as
// a json.Number — its decimal text — rather than a float64.
//
// A float64 holds integers exactly only up to 2^53, and an identifier or an
// amount in cents goes past that. Decoded as a float64, such a value comes back
// rounded, and a subscriber that decodes the payload into its own type reads
// the rounded value with no error — or fails outright on the largest integers
// — where an in-process publish would have handed it the exact one. Kept as
// its text, a number serializes back to what the publisher wrote, so every
// payload helper reads it as it reads the in-process shape.
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

// Deliver runs every handler subscribed to evt's type, in subscription order,
// on ctx. Each is isolated from the others: a returned error or a panic is
// logged and the next handler still runs, and neither reaches the caller.
func (t *Table) Deliver(ctx context.Context, evt event.Event) {
	t.mu.Lock()
	handlers := slices.Clone(t.handlers[evt.Type])
	t.mu.Unlock()
	for i, h := range handlers {
		if err := run(ctx, h, evt); err != nil {
			t.logger.Error(t.name+": a subscriber failed; the publisher is not told",
				"event", evt.Type, "handler", i, "err", err)
		}
	}
}

// run isolates one handler from the goroutine that runs it and from the
// handlers after it.
func run(ctx context.Context, h event.Handler, evt event.Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the handler panicked: %v", r)
		}
	}()
	return h(ctx, evt)
}

// Go runs one reader of a transport-backed bus on a goroutine of its own.
//
// reading is cancelled when the stop stage begins, and a reader takes nothing
// more from its backend once it is. handling is what the reader runs the
// subscribers on, and what it acknowledges the entries it delivered under; it
// is cancelled only when the stop stage's grace runs out, and a reader that
// finds it cancelled delivers nothing more.
//
// A reader is started only between Freeze and the stop stage: one arriving
// later has nothing to deliver to.
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
// readers take nothing more, and the subscribers already running are given
// Grace to finish on a context that is still live.
//
// A reader that is holding entries it took before the stop delivers and
// acknowledges them within the same grace, so a normal stop leaves nothing it
// had taken unacknowledged. When the grace runs out, the subscribers' context
// is cancelled, the readers are given a moment more to return, and the error
// says what was cut short.
//
// It is idempotent, and it tolerates a table that was never frozen: a startup
// that failed part-way stops every module it built, whatever stage it reached.
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

// readersGone waits up to d for every reader to return, and reports whether
// they all did.
//
// A reader is added only while the table accepts, and the stop stage leaves
// that stage under the lock before it waits, so no reader is added once a
// wait has begun.
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
