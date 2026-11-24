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

func (r *relaxRig) deliverChange(change settings.Change) {
	_ = r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

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

// TestARelaxingChangeWritesAWarnAndAnAuditRow checks that a relaxing change writes one Warn and one best-effort audit row.
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

// TestATighteningOrUnwatchedChangeWritesNothing checks that a tightening change and an unwatched setting write nothing.
func TestATighteningOrUnwatchedChangeWritesNothing(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: globalRequiredPath, Old: "false", New: "true"})
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "20m0s", New: "10m0s"})
	r.deliverChange(settings.Change{Key: sensitiveMinAgePath, Old: "12h0m0s", New: "24h0m0s"})
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "10m0s", New: "10m0s"})
	r.deliverChange(settings.Change{Key: maxPerKindPath, Old: "1", New: "20"})
	if got := r.relaxWarns(); len(got) != 0 {
		t.Errorf("a tightening or unwatched change was reported:\n%s", r.logs.String())
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a tightening or unwatched change wrote audit rows: %+v", rows)
	}
}

// TestASensitiveChangeIsIgnoredEntirely checks that a withheld sensitive change writes no Warn and no audit row.
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

// TestADeletionThatFallsBackRelaxes checks that deleting an override that checked more than the default counts as a relaxation.
func TestADeletionThatFallsBackRelaxes(t *testing.T) {
	r := newRelaxRig(t)
	r.deliverChange(settings.Change{Key: globalRequiredPath, Old: "true", New: "", Deleted: true})
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "5m0s", New: "", Deleted: true})
	r.deliverChange(settings.Change{Key: sensitiveMinAgePath, Old: "48h0m0s", New: "", Deleted: true})
	if rows := r.audit.entries(); len(rows) != 3 {
		t.Errorf("the audit holds %d rows, want 3 for the three deletions", len(rows))
	}
}

// TestTheMonitorIsInertWithoutASettingsStore checks that without a settings store the monitor subscribes to nothing and is inert.
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

// TestAnAuditWriteFailureStillWarnsAndDoesNotFail checks that an audit write failure still warns and does not fail the handler.
func TestAnAuditWriteFailureStillWarnsAndDoesNotFail(t *testing.T) {
	r := newRelaxRig(t)
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: recentWindowPath, Old: "10m0s", New: "20m0s"})
	if got := r.relaxWarns(recentWindowPath, dirIncrease); len(got) != 1 {
		t.Errorf("the Warn was lost when the audit write failed:\n%s", r.logs.String())
	}
}
