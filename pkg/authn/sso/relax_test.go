package sso

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// relaxBus is a bus that keeps its subscriptions and delivers an event to the
// handlers of its type, which is what a process-local bus does.
type relaxBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newRelaxBus() *relaxBus { return &relaxBus{handlers: map[string][]event.Handler{}} }

func (b *relaxBus) Subscribe(eventType string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *relaxBus) Publish(context.Context, event.Event) error { return nil }

func (b *relaxBus) deliver(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	hs := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	var errs []error
	for _, h := range hs {
		errs = append(errs, h(ctx, evt))
	}
	return errors.Join(errs...)
}

// relaxAudit keeps the rows it was asked to write.
type relaxAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *relaxAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
}

func (a *relaxAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

// relaxStore is the settings store, present only so that the module subscribes:
// the relax handler reads the change off the event and never the store.
type relaxStore struct {
	settings.Store
}

// relaxRig is the module's product with the seams a relax case drives.
type relaxRig struct {
	p     *product
	bus   *relaxBus
	audit *relaxAudit
	store *relaxStore
	logs  *logBuffer
}

// newRelaxRig builds a product with the event bus, the audit and the settings
// store present and the subscription wired, as Init would.
func newRelaxRig(t *testing.T) *relaxRig {
	t.Helper()
	logs := &logBuffer{}
	p := &product{logger: logs.logger()}
	r := &relaxRig{p: p, bus: newRelaxBus(), audit: &relaxAudit{}, store: &relaxStore{}, logs: logs}
	p.bus = r.bus
	p.audit = r.audit
	p.store = r.store
	p.subscribe()
	return r
}

// deliverChange hands a settings.changed event to the module's subscription.
func (r *relaxRig) deliverChange(change settings.Change) {
	_ = r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow holds the visibility: a setting
// moved in its relaxing direction writes exactly one Warn naming the key and the
// direction and one best-effort audit row, and nothing else.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "8m0s", Actor: "op"})

	warns := r.logs.lines("WARN", "setting="+assertionMaxAgePath, "direction="+dirIncrease)
	if len(warns) != 1 {
		t.Errorf("the relaxing change wrote %d Warn lines, want 1; the log was:\n%s", len(warns), r.logs.String())
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Action != relaxedAction || row.Object != assertionMaxAgePath || row.Outcome != relaxedOutcome {
		t.Errorf("audit row = %+v, want the relaxed action on %s", row, assertionMaxAgePath)
	}
	if got := row.Detail["change"]; got != dirIncrease {
		t.Errorf("the audit detail change = %q, want %q", got, dirIncrease)
	}
}

// TestATighteningChangeWritesNothing holds the other direction: a move that
// checks more is not a relaxation and is not reported, and a move that keeps the
// value the same is not one either.
func TestATighteningChangeWritesNothing(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "8m0s", New: "5m0s"})
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "5m0s"})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a tightening or equal change wrote audit rows: %+v", rows)
	}
	if got := r.logs.lines("WARN"); len(got) != 0 {
		t.Errorf("a tightening or equal change wrote a Warn:\n%s", r.logs.String())
	}
}

// TestAnUnwatchedOrSensitiveChangeIsIgnored holds the filter: a key this module
// does not watch and a change whose value was withheld are both dropped.
func TestAnUnwatchedOrSensitiveChangeIsIgnored(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: "authn.sso.provider-cache-ttl", Old: "1m0s", New: "1h0m0s"})
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "8m0s", Sensitive: true})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("an unwatched or sensitive change wrote audit rows: %+v", rows)
	}
	if got := r.logs.lines("WARN"); len(got) != 0 {
		t.Errorf("an unwatched or sensitive change wrote a Warn:\n%s", r.logs.String())
	}
}

// TestADeletedLayerReadsAsTheDeclaredDefault holds the comparison against the
// declared default: raising the window above it relaxes, and dropping the row
// back to it does not.
func TestADeletedLayerReadsAsTheDeclaredDefault(t *testing.T) {
	r := newRelaxRig(t)
	// The row was removed, so the layer reads as the declared 5m: from 8m that
	// tightens.
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "8m0s", Deleted: true})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a delete that tightens to the default wrote audit rows: %+v", rows)
	}
	// A first write above the default relaxes against the absent layer.
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "", New: "8m0s"})
	if rows := r.audit.entries(); len(rows) != 1 {
		t.Errorf("a write above the declared default wrote %d audit rows, want 1", len(rows))
	}
}

// TestAnAuditWriteFailureStillWarns holds the best-effort direction: a failed
// audit write leaves the Warn written and the handler carrying on.
func TestAnAuditWriteFailureStillWarns(t *testing.T) {
	r := newRelaxRig(t)
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "8m0s"})
	if got := r.logs.lines("WARN", "direction="+dirIncrease); len(got) != 1 {
		t.Errorf("a failed audit write dropped the Warn; the log was:\n%s", r.logs.String())
	}
}

// TestWithNoBusOrStoreTheSubscriptionIsInert holds the gating: with either the
// event bus or the settings store absent the module registers no subscription,
// so a relaxing change is neither reported nor a cause to panic.
func TestWithNoBusOrStoreTheSubscriptionIsInert(t *testing.T) {
	t.Run("no settings store", func(t *testing.T) {
		logs := &logBuffer{}
		bus := newRelaxBus()
		sink := &relaxAudit{}
		p := &product{logger: logs.logger(), bus: bus, audit: sink}
		// store is absent.
		p.subscribe()
		if err := bus.deliver(context.Background(), event.Event{
			Type:    settings.ChangedEventType,
			Payload: settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "8m0s"},
		}); err != nil {
			t.Errorf("delivering with no settings store returned %v", err)
		}
		if rows := sink.entries(); len(rows) != 0 {
			t.Errorf("a change was audited with no settings store: %+v", rows)
		}
		if got := logs.lines("WARN"); len(got) != 0 {
			t.Errorf("a change was reported with no settings store:\n%s", logs.String())
		}
	})
	t.Run("no event bus", func(t *testing.T) {
		p := &product{logger: (&logBuffer{}).logger(), store: &relaxStore{}}
		p.subscribe() // must not panic with no bus
	})
	t.Run("the handler alone never panics", func(t *testing.T) {
		p := &product{logger: (&logBuffer{}).logger()}
		if err := p.onSettingsChanged(context.Background(), event.Event{
			Type:    settings.ChangedEventType,
			Payload: settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "8m0s"},
		}); err != nil {
			t.Errorf("the handler returned %v with no seams", err)
		}
	})
}
