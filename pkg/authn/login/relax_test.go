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

type subscribeBus struct {
	event.Bus
	mu    sync.Mutex
	types []string
}

func (b *subscribeBus) Subscribe(eventType string, _ string, _ event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.types = append(b.types, eventType)
}

func (b *subscribeBus) subscribed() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.types...)
}

type stubStore struct{ settings.Store }

func changeEvent(change settings.Change) event.Event {
	return event.Event{Type: settings.ChangedEventType, Payload: change}
}

func deliver(t *testing.T, p *product, change settings.Change) {
	t.Helper()
	if err := p.onSettingsChanged(context.Background(), changeEvent(change)); err != nil {
		t.Fatalf("the handler returned %v, want nil", err)
	}
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow holds that a relaxing move writes one Warn and one best-effort audit row, and nothing else.
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

// TestATighteningOrEqualChangeWritesNothing holds that a move checking more, or one keeping the value, is not reported.
func TestATighteningOrEqualChangeWritesNothing(t *testing.T) {
	changes := []settings.Change{
		{Key: flowTTLPath, Old: "8m0s", New: "5m0s"},
		{Key: flowTTLPath, Old: "5m0s", New: "5m0s"},
		{Key: disabledMethodsPath, Old: `[]`, New: `["sms"]`},
		{Key: disabledMethodsPath, Old: `["password"]`, New: `[]`},
		{Key: deliveryModesPath, Old: `["session","token"]`, New: `["session"]`},
		{Key: ipLimitPath, Old: "0", New: "5"},
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

// TestAnUnwatchedOrSensitiveChangeIsIgnored holds that an unwatched key and a withheld value are dropped.
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

// TestARelaxationWithoutAnAuditSeamStillWarns holds that the Warn is written with or without the audit seam.
func TestARelaxationWithoutAnAuditSeamStillWarns(t *testing.T) {
	logs := &relaxLogs{}
	p := &product{logger: slog.New(logs)}
	deliver(t, p, settings.Change{Key: flowTTLPath, Old: "5m0s", New: "8m0s"})
	if got := logs.warns(); len(got) != 1 {
		t.Errorf("the Warn lines are %d, want 1 without an audit seam", len(got))
	}
}

// TestAnAuditWriteFailureLeavesTheHandlerSucceeding holds that a failed audit write still writes the Warn and does not fail the change.
func TestAnAuditWriteFailureLeavesTheHandlerSucceeding(t *testing.T) {
	logs := &relaxLogs{}
	sink := &relaxAudit{err: errors.New("the audit is down")}
	p := &product{logger: slog.New(logs), audit: sink}
	deliver(t, p, settings.Change{Key: flowTTLPath, Old: "5m0s", New: "8m0s"})
	if got := logs.warns(); len(got) != 1 {
		t.Errorf("the Warn lines are %d, want 1 when the audit fails", len(got))
	}
}

// TestSubscriptionIsWiredOnlyWithABusAndAStore holds that the subscription is inert when either the bus or the store is absent.
func TestSubscriptionIsWiredOnlyWithABusAndAStore(t *testing.T) {
	t.Run("neither present", func(t *testing.T) {
		p := &product{logger: slog.New(&relaxLogs{})}
		p.subscribe()
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
		p.subscribe()
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
