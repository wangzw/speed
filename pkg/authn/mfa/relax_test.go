package mfa

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// relaxRig is the module with a settings store, an event bus and an audit seam
// wired the way Init wires them, and no database: the relaxation monitor reads
// none of the module's own tables.
type relaxRig struct {
	p     *product
	bus   *recordingBus
	audit *recordingAudit
	store *fakeStore
	logs  *logBuffer
}

func newRelaxRig(t *testing.T) *relaxRig {
	t.Helper()
	r := &relaxRig{bus: newBus(), audit: &recordingAudit{}, store: newStore(), logs: &logBuffer{}}
	p := build(nil, nil, nil, nil, nil)
	p.logger = r.logs.logger()
	p.settings = newReader(r.store, p.logger)
	p.audit = r.audit
	p.bus = r.bus
	p.subscribe(r.bus)
	r.p = p
	return r
}

// deliverChange hands a settings.changed event to the module's subscription, as a
// replica that received the change would.
func (r *relaxRig) deliverChange(change settings.Change) {
	_ = r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

// relaxWarns returns the Warn lines of the relaxation monitor that carry every
// one of the substrings.
func (r *relaxRig) relaxWarns(substrings ...string) []string {
	var out []string
	for _, line := range strings.Split(r.logs.String(), "\n") {
		if !strings.Contains(line, "level=WARN") || !strings.Contains(line, "checks less than before") {
			continue
		}
		ok := line != ""
		for _, s := range substrings {
			ok = ok && strings.Contains(line, s)
		}
		if ok {
			out = append(out, line)
		}
	}
	return out
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow holds the visibility: for every
// watched setting, a change that moves it in its relaxing direction writes one
// Warn naming the key and the direction and one best-effort audit row, and
// nothing else.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	cases := []struct{ key, old, new, direction string }{
		{globalRequiredPath, "true", "false", dirToFalse},
		{recentWindowPath, "10m0s", "20m0s", dirIncrease},
		{sensitiveMinAgePath, "24h0m0s", "12h0m0s", dirDecrease},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			r := newRelaxRig(t)
			r.deliverChange(settings.Change{Key: c.key, Old: c.old, New: c.new, Actor: "op"})

			if got := r.relaxWarns(c.key, c.direction); len(got) != 1 {
				t.Errorf("the relaxation monitor wrote %d Warns for %s; the log was:\n%s", len(got), c.key, r.logs.String())
			}
			rows := r.audit.entries()
			if len(rows) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(rows))
			}
			if rows[0].Action != relaxedAction || rows[0].Object != c.key || rows[0].Outcome != relaxedOutcome {
				t.Errorf("audit row = %+v, want the relaxed action on %s", rows[0], c.key)
			}
			if rows[0].Detail["change"] != c.direction {
				t.Errorf("the audit detail = %v, want the direction", rows[0].Detail)
			}
		})
	}
}

// TestATighteningOrUnwatchedChangeWritesNothing holds the other direction and
// the filter: a move that checks more is not a relaxation, and a setting this
// module does not watch is not reported.
func TestATighteningOrUnwatchedChangeWritesNothing(t *testing.T) {
	r := newRelaxRig(t)
	// Turning the switch on, narrowing the window and lengthening the age all
	// check more, not less.
	r.deliverChange(settings.Change{Key: globalRequiredPath, Old: "false", New: "true"})
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "20m0s", New: "10m0s"})
	r.deliverChange(settings.Change{Key: sensitiveMinAgePath, Old: "12h0m0s", New: "24h0m0s"})
	// A change that keeps a value the same relaxes nothing.
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "10m0s", New: "10m0s"})
	// A setting this module does not watch.
	r.deliverChange(settings.Change{Key: maxPerKindPath, Old: "1", New: "20"})
	if got := r.relaxWarns(); len(got) != 0 {
		t.Errorf("a tightening or unwatched change was reported:\n%s", r.logs.String())
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a tightening or unwatched change wrote audit rows: %+v", rows)
	}
}

// TestASensitiveChangeIsIgnoredEntirely holds the Sensitive filter against a
// change whose values would relax: a withheld change is dropped before the
// relaxation test, so it writes no Warn and no audit row.
func TestASensitiveChangeIsIgnoredEntirely(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: globalRequiredPath, Old: "true", New: "false", Sensitive: true})
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "10m0s", New: "20m0s", Sensitive: true})
	if got := r.relaxWarns(); len(got) != 0 {
		t.Errorf("a sensitive change wrote a Warn:\n%s", r.logs.String())
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", rows)
	}
}

// TestADeletionThatFallsBackRelaxes holds the comparison over the declared
// default: a watched setting whose row is deleted reads as its declared default,
// so removing an override that checked more than the default is itself a
// relaxation.
func TestADeletionThatFallsBackRelaxes(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: globalRequiredPath, Old: "true", New: "", Deleted: true})
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "5m0s", New: "", Deleted: true})
	r.deliverChange(settings.Change{Key: sensitiveMinAgePath, Old: "48h0m0s", New: "", Deleted: true})
	if rows := r.audit.entries(); len(rows) != 3 {
		t.Errorf("the audit holds %d rows, want 3 for the three deletions", len(rows))
	}
}

// TestTheMonitorIsInertWithoutASettingsStore holds the guard: without a settings
// store there is no setting to watch, so the settings.changed handler is not
// subscribed and a delivered change leaves no trace, and the handler invoked
// directly is inert and does not panic.
func TestTheMonitorIsInertWithoutASettingsStore(t *testing.T) {
	logs := &logBuffer{}
	p := build(nil, nil, nil, nil, nil)
	p.logger = logs.logger()

	bus := newBus()
	p.subscribe(bus)
	if types := bus.subscribedTypes(); slices.Contains(types, settings.ChangedEventType) {
		t.Errorf("the module subscribed to the settings change without a settings store: %v", types)
	}
	_ = bus.deliver(context.Background(), event.Event{
		Type: settings.ChangedEventType, Payload: settings.Change{Key: recentWindowPath, Old: "10m0s", New: "20m0s"},
	})
	if got := logs.lines("level=WARN", "checks less than before"); len(got) != 0 {
		t.Errorf("a change was reported without a settings store:\n%s", logs.String())
	}

	if err := p.onSettingsChanged(context.Background(), event.Event{
		Type: settings.ChangedEventType, Payload: settings.Change{Key: recentWindowPath, Old: "10m0s", New: "20m0s"},
	}); err != nil {
		t.Fatalf("the handler without a settings store returned %v, want the event left unfailed", err)
	}
}

// TestAnAuditWriteFailureStillWarnsAndDoesNotFail holds the best-effort
// direction: the change is still reported at Warn and the handler carries on
// rather than failing the event.
func TestAnAuditWriteFailureStillWarnsAndDoesNotFail(t *testing.T) {
	r := newRelaxRig(t)
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "10m0s", New: "20m0s"})
	if got := r.relaxWarns(recentWindowPath, dirIncrease); len(got) != 1 {
		t.Errorf("the Warn was lost when the audit write failed:\n%s", r.logs.String())
	}
}
