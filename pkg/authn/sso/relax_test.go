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

type relaxBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newRelaxBus() *relaxBus { return &relaxBus{handlers: map[string][]event.Handler{}} }

func (b *relaxBus) Subscribe(eventType string, _ string, h event.Handler) {
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

type relaxStore struct {
	settings.Store
}

type relaxRig struct {
	p     *product
	bus   *relaxBus
	audit *relaxAudit
	store *relaxStore
	logs  *logBuffer
}

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

func (r *relaxRig) deliverChange(change settings.Change) {
	_ = r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow checks that a relaxing change writes one Warn and one audit row.
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

// TestATighteningChangeWritesNothing checks that a tightening change writes nothing.
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

// TestAnUnwatchedOrSensitiveChangeIsIgnored checks that an unwatched or withheld change is ignored.
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

// TestADeletedLayerReadsAsTheDeclaredDefault checks that a deleted layer reads as the declared default.
func TestADeletedLayerReadsAsTheDeclaredDefault(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "8m0s", Deleted: true})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a delete that tightens to the default wrote audit rows: %+v", rows)
	}
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "", New: "8m0s"})
	if rows := r.audit.entries(); len(rows) != 1 {
		t.Errorf("a write above the declared default wrote %d audit rows, want 1", len(rows))
	}
}

// TestAnAuditWriteFailureStillWarns checks that an audit write failure still warns.
func TestAnAuditWriteFailureStillWarns(t *testing.T) {
	r := newRelaxRig(t)
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: assertionMaxAgePath, Old: "5m0s", New: "8m0s"})
	if got := r.logs.lines("WARN", "direction="+dirIncrease); len(got) != 1 {
		t.Errorf("a failed audit write dropped the Warn; the log was:\n%s", r.logs.String())
	}
}

// TestWithNoBusOrStoreTheSubscriptionIsInert checks that without a bus or store the subscription is inert.
func TestWithNoBusOrStoreTheSubscriptionIsInert(t *testing.T) {
	t.Run("no settings store", func(t *testing.T) {
		logs := &logBuffer{}
		bus := newRelaxBus()
		sink := &relaxAudit{}
		p := &product{logger: logs.logger(), bus: bus, audit: sink}
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
		p.subscribe()
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
