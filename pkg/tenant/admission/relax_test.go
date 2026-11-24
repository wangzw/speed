package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

type recordingAudit struct {
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.rows = append(a.rows, e)
	return a.err
}

func relaxRig(t *testing.T) (*rig, *recordingAudit) {
	t.Helper()
	r := newRig(t)
	r.withSettings()
	audit := &recordingAudit{}
	r.l.audit = audit
	return r, audit
}

func (r *rig) deliverChange(change settings.Change) {
	_ = r.l.onSettingsChanged(context.Background(), event.Event{
		Type:    settings.ChangedEventType,
		Payload: change,
	})
}

func (r *rig) warnsFor(setting, direction string) (all, withDirection int) {
	for _, rec := range r.logs.atLevel(levelWarn) {
		if rec.attrs["setting"] != setting {
			continue
		}
		all++
		if rec.attrs["direction"] == direction {
			withDirection++
		}
	}
	return all, withDirection
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow checks that a relaxing change to any
// watched setting writes one Warn and one best-effort audit row.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	cases := []struct {
		name      string
		change    settings.Change
		direction string
	}{
		{"require-mfa switched off", settings.Change{Key: requireMFAKey, Old: "true", New: "false", Actor: "op"}, dirToFalse},
		{"require-sso switched off", settings.Change{Key: requireSSOKey, Old: "true", New: "false", Actor: "op"}, dirToFalse},
		{"check-ttl lengthened", settings.Change{Key: checkTTLKey, Old: "5s", New: "30s", Actor: "op"}, dirIncrease},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, audit := relaxRig(t)
			r.deliverChange(c.change)

			all, withDirection := r.warnsFor(c.change.Key, c.direction)
			if all != 1 || withDirection != 1 {
				t.Errorf("%s wrote %d Warns (and %d naming the direction), want exactly one Warn carrying setting and direction; the log was:\n%v",
					c.change.Key, all, withDirection, r.logs.atLevel(levelWarn))
			}
			if len(audit.rows) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(audit.rows))
			}
			row := audit.rows[0]
			if row.Action != relaxedAction || row.Object != c.change.Key || row.Outcome != relaxedOutcome {
				t.Errorf("audit row = %+v, want the relaxed action on %s", row, c.change.Key)
			}
			if row.Detail["change"] != c.direction {
				t.Errorf("the audit detail = %v, want change=%s", row.Detail, c.direction)
			}
		})
	}
}

// TestATighteningOrEqualChangeWritesNothing checks that a change that tightens or
// keeps a setting writes nothing.
func TestATighteningOrEqualChangeWritesNothing(t *testing.T) {
	changes := []settings.Change{
		{Key: requireMFAKey, Old: "false", New: "true"},
		{Key: requireSSOKey, Old: "false", New: "false"},
		{Key: checkTTLKey, Old: "30s", New: "5s"},
		{Key: checkTTLKey, Old: "5s", New: "5s"},
	}
	for _, change := range changes {
		r, audit := relaxRig(t)
		r.deliverChange(change)
		if len(audit.rows) != 0 {
			t.Errorf("a tightening or equal change on %s wrote audit rows: %+v", change.Key, audit.rows)
		}
		if all, _ := r.warnsFor(change.Key, ""); all != 0 {
			t.Errorf("a tightening or equal change on %s wrote %d Warns, want none", change.Key, all)
		}
	}
}

// TestASensitiveChangeIsSkippedEntirely holds that a sensitive change is dropped
// before the relaxation test, writing no Warn and no audit row.
func TestASensitiveChangeIsSkippedEntirely(t *testing.T) {
	r, audit := relaxRig(t)
	r.deliverChange(settings.Change{Key: checkTTLKey, Sensitive: true})
	r.deliverChange(settings.Change{Key: requireMFAKey, Old: "true", New: "false", Sensitive: true})
	if len(audit.rows) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", audit.rows)
	}
	for _, rec := range r.logs.atLevel(levelWarn) {
		t.Errorf("a sensitive change wrote a Warn: %+v", rec)
	}
}

// TestAnUnwatchedChangeIsIgnored holds the membership test: a key this module
// does not watch is dropped whatever its values say.
func TestAnUnwatchedChangeIsIgnored(t *testing.T) {
	r, audit := relaxRig(t)
	r.deliverChange(settings.Change{Key: "authn.mfa.max-per-kind", Old: "1", New: "20"})
	if len(audit.rows) != 0 {
		t.Errorf("an unwatched change wrote audit rows: %+v", audit.rows)
	}
	if all, _ := r.warnsFor("authn.mfa.max-per-kind", ""); all != 0 {
		t.Errorf("an unwatched change wrote %d Warns, want none", all)
	}
}

// TestTheHandlerIsInertAndSafeWithoutTheSeams holds that with no bus and no settings
// store the handler writes nothing and does not panic.
func TestTheHandlerIsInertAndSafeWithoutTheSeams(t *testing.T) {
	r := newRig(t)
	if r.l.store != nil || r.l.bus != nil || r.l.audit != nil {
		t.Fatal("the rig is not the seam-less assembly this case is about")
	}
	if err := r.l.onSettingsChanged(context.Background(), event.Event{
		Type:    settings.ChangedEventType,
		Payload: settings.Change{Key: checkTTLKey, Old: "5s", New: "30s"},
	}); err != nil {
		t.Fatalf("the handler returned %v, want nil", err)
	}
	if all, _ := r.warnsFor(checkTTLKey, ""); all != 0 {
		t.Errorf("the handler wrote %d Warns with no settings store, want none", all)
	}
}

// TestAnAuditWriteFailureIsNotFatal holds that a failed audit write is logged and the
// change is still reported, failing no event.
func TestAnAuditWriteFailureIsNotFatal(t *testing.T) {
	r, audit := relaxRig(t)
	audit.err = errors.New("the audit is down")
	if err := r.l.onSettingsChanged(context.Background(), event.Event{
		Type:    settings.ChangedEventType,
		Payload: settings.Change{Key: checkTTLKey, Old: "5s", New: "30s"},
	}); err != nil {
		t.Fatalf("the handler returned %v, want nil", err)
	}
	if all, _ := r.warnsFor(checkTTLKey, dirIncrease); all != 1 {
		t.Errorf("the change wrote %d Warns, want 1", all)
	}
	if got := len(r.logs.atLevel(levelError)); got != 1 {
		t.Errorf("a failed audit write wrote %d Error lines, want 1; the log was:\n%v", got, r.logs.atLevel(levelError))
	}
}

// TestTheChangeSubscriptionNeedsBothTheBusAndTheStore holds that the change subscription
// is registered only when both the event bus and the settings store are present.
func TestTheChangeSubscriptionNeedsBothTheBusAndTheStore(t *testing.T) {
	t.Run("a store without a bus subscribes to nothing", func(t *testing.T) {
		if err := (fixture{supplied: []supply{withStore(newSettingsStub())}}).drive(t, core.StageInit, nil); err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
	})
	t.Run("a bus without a store does not subscribe to the change event", func(t *testing.T) {
		bus := newBusStub()
		if err := (fixture{supplied: []supply{withBus(bus)}}).drive(t, core.StageInit, nil); err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		if got := bus.subscribed(settings.ChangedEventType); got != 0 {
			t.Errorf("the change event has %d subscriptions without a store, want 0", got)
		}
		if got := bus.subscribed(authn.EventMembershipChanged); got != 1 {
			t.Errorf("the membership event has %d subscriptions, want 1", got)
		}
	})
	t.Run("both present subscribe to the change event", func(t *testing.T) {
		bus := newBusStub()
		if err := (fixture{supplied: []supply{withStore(newSettingsStub()), withBus(bus)}}).drive(t, core.StageInit, nil); err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		if got := bus.subscribed(settings.ChangedEventType); got != 1 {
			t.Errorf("the change event has %d subscriptions with both present, want 1", got)
		}
	})
}
