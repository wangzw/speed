package impersonation

import (
	"context"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

func (r *rig) deliverChange(change settings.Change) {
	_ = r.p.onSettingsChanged(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

func (r *rig) warns(fragments ...string) int {
	count := 0
	for _, line := range strings.Split(r.logs.String(), "\n") {
		if !strings.Contains(line, "level=WARN") {
			continue
		}
		ok := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				ok = false
				break
			}
		}
		if ok {
			count++
		}
	}
	return count
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow checks that each watched setting moved toward lenient writes one Warn and one audit row.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	for _, c := range []struct {
		name     string
		key      string
		old, new string
	}{
		{"a longer max-ttl", maxTTLPath, "30m0s", "59m0s"},
		{"a longer max-auth-age", maxAuthAgePath, "5m0s", "29m0s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, nil)
			r.deliverChange(settings.Change{Key: c.key, Old: c.old, New: c.new})

			if got := r.warns("checks less than before", "setting="+c.key, "direction="+moveUp); got != 1 {
				t.Errorf("the log holds %d Warns naming the relaxed %s, want 1:\n%s", got, c.key, r.logs.String())
			}
			if strings.Contains(r.logs.String(), c.old) || strings.Contains(r.logs.String(), c.new) {
				t.Errorf("a Warn carries a value:\n%s", r.logs.String())
			}

			rows := r.audit.entries()
			if len(rows) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(rows))
			}
			if rows[0].Action != relaxedAction || rows[0].Object != c.key || rows[0].Outcome != relaxedOutcome {
				t.Errorf("audit row = %+v, want the relaxed action on %s", rows[0], c.key)
			}
			if rows[0].Detail["change"] != moveUp {
				t.Errorf("the audit detail = %v, want the direction %q", rows[0].Detail, moveUp)
			}
		})
	}
}

// TestATighteningOrEqualChangeWritesNothing checks that a tightening or equal change is neither warned nor audited.
func TestATighteningOrEqualChangeWritesNothing(t *testing.T) {
	r := newRig(t, nil)
	r.deliverChange(settings.Change{Key: maxTTLPath, Old: "59m0s", New: "30m0s"})
	r.deliverChange(settings.Change{Key: maxAuthAgePath, Old: "29m0s", New: "5m0s"})
	r.deliverChange(settings.Change{Key: maxTTLPath, Old: "30m0s", New: "30m0s"})

	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a tightening or equal change wrote audit rows: %+v", rows)
	}
	if got := r.warns("checks less than before"); got != 0 {
		t.Errorf("a tightening or equal change wrote %d Warns:\n%s", got, r.logs.String())
	}
}

// TestAnUnwatchedOrSensitiveChangeIsIgnored checks that an unwatched key and a withheld value are dropped before the relaxation test.
func TestAnUnwatchedOrSensitiveChangeIsIgnored(t *testing.T) {
	r := newRig(t, nil)
	r.deliverChange(settings.Change{Key: accessTTLPath, Old: "10m0s", New: "59m0s"})
	r.deliverChange(settings.Change{Key: maxTTLPath, Old: "30m0s", New: "59m0s", Sensitive: true})

	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("an unwatched or sensitive change wrote audit rows: %+v", rows)
	}
	if got := r.warns("checks less than before"); got != 0 {
		t.Errorf("an unwatched or sensitive change wrote %d Warns:\n%s", got, r.logs.String())
	}
}

// TestTheSubscriptionNeedsBothBusAndStore checks that the module subscribes only when both the event bus and the settings store are present.
func TestTheSubscriptionNeedsBothBusAndStore(t *testing.T) {
	r := newRig(t, nil)
	r.p.subscribeSettings(r.bus)
	if got := len(r.bus.handlers[settings.ChangedEventType]); got != 1 {
		t.Errorf("%d handlers subscribed with the bus and the store present, want 1", got)
	}

	busless := newRig(t, nil)
	busless.p.subscribeSettings(nil)
	if got := len(busless.bus.handlers[settings.ChangedEventType]); got != 0 {
		t.Errorf("a nil bus took %d subscribers, want none", got)
	}

	storeless := newRig(t, nil)
	storeless.p.settings = newReader(nil, storeless.p.logger)
	storeless.p.subscribeSettings(storeless.bus)
	if got := len(storeless.bus.handlers[settings.ChangedEventType]); got != 0 {
		t.Errorf("a missing store took %d subscribers, want none", got)
	}
}

// TestTheHandlerIsInertWithoutABusOrStore checks that the handler and the subscription are inert in an assembly with no bus, store or audit seam.
func TestTheHandlerIsInertWithoutABusOrStore(t *testing.T) {
	p := build(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if p.bus != nil || p.settings.store != nil || p.audit != nil {
		t.Fatal("the product is not the empty assembly this case is about")
	}
	logs := &logBuffer{}
	p.logger = logs.logger()

	if err := p.onSettingsChanged(context.Background(), event.Event{
		Payload: settings.Change{Key: maxTTLPath, Old: "30m0s", New: "59m0s"},
	}); err != nil {
		t.Fatalf("the handler returned %v, want nil", err)
	}
	p.subscribeSettings(p.bus)
}
