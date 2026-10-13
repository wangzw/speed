package login

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// relaxLogs is a slog.Handler that records each record's level, message and
// attributes, so a case can see what a Warn line carried.
type relaxLogs struct {
	mu   sync.Mutex
	recs []relaxRecord
}

type relaxRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

func (l *relaxLogs) Enabled(context.Context, slog.Level) bool { return true }

func (l *relaxLogs) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, relaxRecord{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}

func (l *relaxLogs) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *relaxLogs) WithGroup(string) slog.Handler      { return l }

// warns is the Warn records written, in order.
func (l *relaxLogs) warns() []relaxRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []relaxRecord
	for _, r := range l.recs {
		if r.level == slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

// relaxAudit is an audit sink that records the entries the handler writes.
type relaxAudit struct {
	audit.Audit
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func (a *relaxAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return a.err
}

func (a *relaxAudit) rows() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// subscribeBus is a bus that records the event types it was asked to subscribe.
type subscribeBus struct {
	event.Bus
	mu    sync.Mutex
	types []string
}

func (b *subscribeBus) Subscribe(eventType string, _ event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.types = append(b.types, eventType)
}

func (b *subscribeBus) subscribed() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.types...)
}

// stubStore satisfies the settings.Store interface with a nil method set; the
// subscription only asks whether it is present and never calls it.
type stubStore struct{ settings.Store }

// changeEvent wraps a settings change as the bus would deliver it.
func changeEvent(change settings.Change) event.Event {
	return event.Event{Type: settings.ChangedEventType, Payload: change}
}

// deliver hands a settings change to the handler.
func deliver(t *testing.T, p *product, change settings.Change) {
	t.Helper()
	if err := p.onSettingsChanged(context.Background(), changeEvent(change)); err != nil {
		t.Fatalf("the handler returned %v, want nil", err)
	}
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow holds the visibility: a setting
// moved in its relaxing direction writes one Warn naming the key and the
// direction and one best-effort audit row, and nothing else.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	watched := []struct {
		key       string
		old, new  string
		direction string
	}{
		{flowTTLPath, "5m0s", "8m0s", dirIncrease},
		{disabledMethodsPath, `["sms"]`, `[]`, dirRemoveMember},
		{deliveryModesPath, `["session"]`, `["session","token"]`, dirAddMember},
		{ipLimitPath, "5", "0", dirToZero},
	}
	for _, c := range watched {
		t.Run(c.key, func(t *testing.T) {
			logs := &relaxLogs{}
			sink := &relaxAudit{}
			p := &product{logger: slog.New(logs), audit: sink}
			deliver(t, p, settings.Change{Key: c.key, Old: c.old, New: c.new})

			warns := logs.warns()
			if len(warns) != 1 {
				t.Fatalf("the Warn lines are %d, want 1", len(warns))
			}
			if len(warns[0].attrs) != 2 {
				t.Errorf("the Warn carried %d fields, want exactly setting and direction: %v", len(warns[0].attrs), warns[0].attrs)
			}
			if warns[0].attrs["setting"] != c.key || warns[0].attrs["direction"] != c.direction {
				t.Errorf("the Warn carried %v, want setting=%s direction=%s", warns[0].attrs, c.key, c.direction)
			}

			rows := sink.rows()
			if len(rows) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(rows))
			}
			if rows[0].Action != relaxedAction || rows[0].Object != c.key || rows[0].Outcome != relaxedOutcome {
				t.Errorf("the audit row = %+v, want the relaxed action on %s", rows[0], c.key)
			}
			if rows[0].Detail["change"] != c.direction {
				t.Errorf("the audit detail = %v, want change=%s", rows[0].Detail, c.direction)
			}
		})
	}
}

// TestATighteningOrEqualChangeWritesNothing holds the strictness of the
// comparison: a move that checks more, and one that keeps a value the same,
// relax nothing and are not reported.
func TestATighteningOrEqualChangeWritesNothing(t *testing.T) {
	changes := []settings.Change{
		{Key: flowTTLPath, Old: "8m0s", New: "5m0s"},                             // a shorter-lived flow
		{Key: flowTTLPath, Old: "5m0s", New: "5m0s"},                             // no change
		{Key: disabledMethodsPath, Old: `[]`, New: `["sms"]`},                    // a method turned off
		{Key: deliveryModesPath, Old: `["session","token"]`, New: `["session"]`}, // a delivery mode removed
		{Key: ipLimitPath, Old: "0", New: "5"},                                   // the pre-throttle turned on
	}
	for _, change := range changes {
		logs := &relaxLogs{}
		sink := &relaxAudit{}
		p := &product{logger: slog.New(logs), audit: sink}
		deliver(t, p, change)
		if got := logs.warns(); len(got) != 0 || len(sink.rows()) != 0 {
			t.Errorf("a tightening or equal change on %s wrote warns %v and rows %v", change.Key, got, sink.rows())
		}
	}
}

// TestAnUnwatchedOrSensitiveChangeIsIgnored holds the filter: a key this module
// does not watch and a change whose value was withheld are both dropped, even
// when the withheld change would have relaxed.
func TestAnUnwatchedOrSensitiveChangeIsIgnored(t *testing.T) {
	logs := &relaxLogs{}
	sink := &relaxAudit{}
	p := &product{logger: slog.New(logs), audit: sink}
	deliver(t, p, settings.Change{Key: "authn.mfa.max-per-kind", Old: "1", New: "20"})
	deliver(t, p, settings.Change{Key: flowTTLPath, Old: "5m0s", New: "8m0s", Sensitive: true})
	if got := logs.warns(); len(got) != 0 || len(sink.rows()) != 0 {
		t.Errorf("an unwatched or sensitive change wrote warns %v and rows %v", got, sink.rows())
	}
}

// TestARelaxationWithoutAnAuditSeamStillWarns holds the best-effort direction:
// the Warn is written whether or not the audit seam is present, and an audit
// write that fails leaves the handler succeeding.
func TestARelaxationWithoutAnAuditSeamStillWarns(t *testing.T) {
	logs := &relaxLogs{}
	p := &product{logger: slog.New(logs)}
	deliver(t, p, settings.Change{Key: flowTTLPath, Old: "5m0s", New: "8m0s"})
	if got := logs.warns(); len(got) != 1 {
		t.Errorf("the Warn lines are %d, want 1 without an audit seam", len(got))
	}
}

// TestAnAuditWriteFailureLeavesTheHandlerSucceeding holds the other best-effort
// direction: the Warn is still written and the change is not failed when the
// audit record cannot be written.
func TestAnAuditWriteFailureLeavesTheHandlerSucceeding(t *testing.T) {
	logs := &relaxLogs{}
	sink := &relaxAudit{err: errors.New("the audit is down")}
	p := &product{logger: slog.New(logs), audit: sink}
	deliver(t, p, settings.Change{Key: flowTTLPath, Old: "5m0s", New: "8m0s"})
	if got := logs.warns(); len(got) != 1 {
		t.Errorf("the Warn lines are %d, want 1 when the audit fails", len(got))
	}
}

// TestSubscriptionIsWiredOnlyWithABusAndAStore holds the guard: the module
// subscribes to the settings change only when both the event bus and the
// settings store are present, and is inert and silent when either is absent.
func TestSubscriptionIsWiredOnlyWithABusAndAStore(t *testing.T) {
	t.Run("neither present", func(t *testing.T) {
		p := &product{logger: slog.New(&relaxLogs{})}
		p.subscribe() // must not panic
	})
	t.Run("a bus with no store", func(t *testing.T) {
		bus := &subscribeBus{}
		p := &product{logger: slog.New(&relaxLogs{}), bus: bus}
		p.subscribe()
		if got := bus.subscribed(); len(got) != 0 {
			t.Errorf("a bus with no store subscribed %v, want none", got)
		}
	})
	t.Run("a store with no bus", func(t *testing.T) {
		p := &product{logger: slog.New(&relaxLogs{}), store: stubStore{}}
		p.subscribe() // must not panic
	})
	t.Run("both present", func(t *testing.T) {
		bus := &subscribeBus{}
		p := &product{logger: slog.New(&relaxLogs{}), bus: bus, store: stubStore{}}
		p.subscribe()
		got := bus.subscribed()
		if len(got) != 1 || got[0] != settings.ChangedEventType {
			t.Errorf("with both present the subscriptions are %v, want [%s]", got, settings.ChangedEventType)
		}
	})
}
