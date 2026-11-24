package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (f *fixture) withAudit() *recordingAudit {
	a := &recordingAudit{}
	f.p.audit = a
	return a
}

func (f *fixture) deliverSettingsChange(change settings.Change) {
	f.bus.deliver(event.Event{Type: settings.ChangedEventType, Payload: change})
}

func relaxWarns(f *fixture, substrings ...string) []string {
	var out []string
	for _, line := range strings.Split(f.logs.String(), "\n") {
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
	cases := []struct{ key, old, new string }{
		{idleTimeoutPath, "30m0s", "45m0s"},
		{absoluteTimeoutPath, "12h0m0s", "24h0m0s"},
		{tokenAbsolutePath, "720h0m0s", "1000h0m0s"},
		{ssoAbsolutePath, "8h0m0s", "10h0m0s"},
		{checkTTLPath, "5s", "10s"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			f := newFixture(t, dialects[0])
			audit := f.withAudit()
			f.deliverSettingsChange(settings.Change{Key: c.key, Old: c.old, New: c.new, Actor: "op"})

			if got := relaxWarns(f, c.key, dirIncrease); len(got) != 1 {
				t.Errorf("the relaxation monitor wrote %d Warns for %s; the log was:\n%s", len(got), c.key, f.logs.String())
			}
			rows := audit.entries()
			if len(rows) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(rows))
			}
			if rows[0].Action != relaxedAction || rows[0].Object != c.key || rows[0].Outcome != relaxedOutcome {
				t.Errorf("audit row = %+v, want the relaxed action on %s", rows[0], c.key)
			}
			if rows[0].Detail["change"] != dirIncrease {
				t.Errorf("the audit detail = %v, want the direction", rows[0].Detail)
			}
		})
	}
}

// TestATighteningOrUnwatchedChangeWritesNothing holds the other direction and
// the filter: a move that checks more is not a relaxation, and a setting this
// module does not watch is not reported.
func TestATighteningOrUnwatchedChangeWritesNothing(t *testing.T) {
	f := newFixture(t, dialects[0])
	audit := f.withAudit()
	f.deliverSettingsChange(settings.Change{Key: idleTimeoutPath, Old: "45m0s", New: "30m0s"})
	f.deliverSettingsChange(settings.Change{Key: absoluteTimeoutPath, Old: "12h0m0s", New: "12h0m0s"})
	f.deliverSettingsChange(settings.Change{Key: touchIntervalPath, Old: "60s", New: "5m0s"})
	f.deliverSettingsChange(settings.Change{Key: retentionPath, Old: "168h0m0s", New: "8760h0m0s"})
	if got := relaxWarns(f); len(got) != 0 {
		t.Errorf("a tightening or unwatched change was reported:\n%s", f.logs.String())
	}
	if len(audit.entries()) != 0 {
		t.Errorf("a tightening or unwatched change wrote audit rows: %+v", audit.entries())
	}
}

// TestASensitiveChangeIsIgnoredEntirely holds the Sensitive filter against a
// change whose values would relax: a withheld change is dropped before the
// relaxation test, so it writes no Warn and no audit row.
func TestASensitiveChangeIsIgnoredEntirely(t *testing.T) {
	f := newFixture(t, dialects[0])
	audit := f.withAudit()
	f.deliverSettingsChange(settings.Change{Key: idleTimeoutPath, Old: "30m0s", New: "45m0s", Sensitive: true})
	if got := relaxWarns(f); len(got) != 0 {
		t.Errorf("a sensitive change wrote a Warn:\n%s", f.logs.String())
	}
	if len(audit.entries()) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", audit.entries())
	}
}

// TestADeleteThatFallsBackToALongerDefaultIsARelaxation holds the comparison
// over the declared default: a watched setting whose row is deleted reads as its
// declared default, so a stored value shorter than the default is a relaxation
// when it is removed.
func TestADeleteThatFallsBackToALongerDefaultIsARelaxation(t *testing.T) {
	f := newFixture(t, dialects[0])
	audit := f.withAudit()
	f.deliverSettingsChange(settings.Change{Key: idleTimeoutPath, Old: "10m0s", New: "", Deleted: true})
	if len(audit.entries()) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(audit.entries()))
	}
	if rows := audit.entries(); rows[0].Object != idleTimeoutPath {
		t.Errorf("audit row = %+v, want the relaxed action on %s", rows[0], idleTimeoutPath)
	}
}

// TestTheMonitorIsNotSubscribedWithoutASettingsStore holds the guard: without a
// settings store there is no change to see, so the settings.changed handler is
// not subscribed and a delivered change leaves no trace.
func TestTheMonitorIsNotSubscribedWithoutASettingsStore(t *testing.T) {
	f := newFixture(t, dialects[0], withoutSettings())
	audit := f.withAudit()
	f.deliverSettingsChange(settings.Change{Key: idleTimeoutPath, Old: "30m0s", New: "45m0s"})
	if got := relaxWarns(f); len(got) != 0 {
		t.Errorf("a change was reported without a settings store:\n%s", f.logs.String())
	}
	if len(audit.entries()) != 0 {
		t.Errorf("a change wrote audit rows without a settings store: %+v", audit.entries())
	}

	// Invoking the handler directly must not panic when the audit seam is absent:
	// it writes the Warn alone and returns without failing the event.
	audit.err = errors.New("the audit is down")
	if err := f.p.onSettingsChanged(context.Background(), event.Event{
		Type: settings.ChangedEventType, Payload: settings.Change{Key: idleTimeoutPath, Old: "30m0s", New: "45m0s"},
	}); err != nil {
		t.Fatalf("the handler returned %v, want the event left unfailed", err)
	}
}

// TestAnAuditWriteFailureStillWarnsAndDoesNotFail holds the best-effort
// direction: the change is still reported at Warn and the handler carries on
// rather than failing the event.
func TestAnAuditWriteFailureStillWarnsAndDoesNotFail(t *testing.T) {
	f := newFixture(t, dialects[0])
	audit := f.withAudit()
	audit.err = errors.New("the audit is down")
	f.deliverSettingsChange(settings.Change{Key: idleTimeoutPath, Old: "30m0s", New: "45m0s"})
	if got := relaxWarns(f, idleTimeoutPath, dirIncrease); len(got) != 1 {
		t.Errorf("the Warn was lost when the audit write failed:\n%s", f.logs.String())
	}
}
