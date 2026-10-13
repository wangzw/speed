package token

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// watched are the settings this module watches, with the direction each relaxes
// in. They are written out here rather than read from the table so the test
// names the keys the design lists, not whatever the table happens to hold.
var watched = []struct {
	key    string
	before string
	after  string
}{
	{accessTTLPath, "10m0s", "20m0s"},
	{leewayPath, "30s", "60s"},
	{refreshIdleTTLPath, "168h0m0s", "200h0m0s"},
	{refreshGracePath, "0s", "30s"},
}

// feedChange hands a settings.ChangedEventType event carrying the change to the
// module's subscription, as a bus delivery would.
func feedChange(r *rig, change settings.Change) {
	_ = r.p.onSettingsChanged(context.Background(), event.Event{
		Type:    settings.ChangedEventType,
		Payload: change,
	})
}

// assertRelaxed holds the whole observable of one relaxing change: exactly one
// Warn naming the key and the direction, and exactly one audit row with the
// relaxed action, the key as its object, the relaxed outcome and the direction
// under the change detail.
func assertRelaxed(t *testing.T, r *rig, key string) {
	t.Helper()
	logs := r.logs.String()
	if n := strings.Count(logs, "level=WARN"); n != 1 {
		t.Fatalf("the log holds %d Warn lines, want 1:\n%s", n, logs)
	}
	if !strings.Contains(logs, "setting="+key) {
		t.Errorf("the Warn line does not name the setting %s:\n%s", key, logs)
	}
	if !strings.Contains(logs, "direction="+dirIncrease) {
		t.Errorf("the Warn line does not carry the direction %s:\n%s", dirIncrease, logs)
	}
	if len(r.audit.entries) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(r.audit.entries))
	}
	entry := r.audit.entries[0]
	if entry.Action != relaxedAction || entry.Object != key || entry.Outcome != relaxedOutcome {
		t.Errorf("audit row = %+v, want the relaxed action on %s", entry, key)
	}
	if entry.Detail["change"] != dirIncrease {
		t.Errorf("the audit detail = %v, want change=%s", entry.Detail, dirIncrease)
	}
}

// TestEachWatchedSettingThatRelaxesWritesAWarnAndAnAuditRow holds the
// visibility for every watched key: a move in its relaxing direction writes one
// Warn and one best-effort audit row, and nothing else.
func TestEachWatchedSettingThatRelaxesWritesAWarnAndAnAuditRow(t *testing.T) {
	for _, c := range watched {
		t.Run(c.key, func(t *testing.T) {
			r := newRig(t)
			feedChange(r, settings.Change{Key: c.key, Old: c.before, New: c.after, Actor: "op"})
			assertRelaxed(t, r, c.key)
		})
	}
}

// TestATighteningOrUnwatchedChangeWritesNothing holds the other directions: a
// move that checks more, a move that keeps the value the same and a key this
// module does not watch are all left alone.
func TestATighteningOrUnwatchedChangeWritesNothing(t *testing.T) {
	r := newRig(t)
	for _, c := range watched {
		feedChange(r, settings.Change{Key: c.key, Old: c.after, New: c.before})
	}
	feedChange(r, settings.Change{Key: leewayPath, Old: "30s", New: "30s"})
	feedChange(r, settings.Change{Key: "authn.session.touch-interval", Old: "1m0s", New: "10m0s"})
	if len(r.audit.entries) != 0 {
		t.Errorf("a tightening, equal or unwatched change wrote audit rows: %+v", r.audit.entries)
	}
	if logs := r.logs.String(); strings.Contains(logs, "level=WARN") {
		t.Errorf("a tightening, equal or unwatched change wrote a Warn:\n%s", logs)
	}
}

// TestASensitiveChangeIsSkippedEntirely holds the filter against a change whose
// values were withheld: it is dropped before the relaxation test, so a withheld
// change that would relax writes no Warn and no audit row.
func TestASensitiveChangeIsSkippedEntirely(t *testing.T) {
	r := newRig(t)
	feedChange(r, settings.Change{Key: accessTTLPath, Old: "10m0s", New: "20m0s", Sensitive: true})
	if len(r.audit.entries) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", r.audit.entries)
	}
	if logs := r.logs.String(); strings.Contains(logs, "level=WARN") {
		t.Errorf("a sensitive change wrote a Warn:\n%s", logs)
	}
}

// TestADeleteThatFallsBackToAMorePermissiveDefaultIsReported holds the reading
// of a layer that held no row: a delete reverses to the declared default, and
// when that default checks less than the value it replaced the change is a
// relaxation. leeway's default (30s) is wider than the removed 0s.
func TestADeleteThatFallsBackToAMorePermissiveDefaultIsReported(t *testing.T) {
	r := newRig(t)
	feedChange(r, settings.Change{Key: leewayPath, Old: "0s", Deleted: true})
	assertRelaxed(t, r, leewayPath)
}

// TestAnAuditWriteFailureOfARelaxedChangeIsCounted holds the best-effort
// direction: the change is still reported and the module carries on, and the
// failed audit write is counted on the audit-write-failures instrument.
func TestAnAuditWriteFailureOfARelaxedChangeIsCounted(t *testing.T) {
	r := newRig(t)
	collected := r.tokenMeter()
	r.audit.err = errors.New("the audit is down")
	feedChange(r, settings.Change{Key: accessTTLPath, Old: "10m0s", New: "20m0s"})

	var rm metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if got := auditFailurePoints(t, rm); got[false] != 1 {
		t.Errorf("the audit-failure counter reported %v, want one point with mandatory=false", got)
	}
}

// TestNoSettingsWatchWithoutAStore holds the wiring guard: the settings watch
// needs the store the settings live in, so without one the bus carries the
// cache-clearing subscriptions alone and never the settings watch.
func TestNoSettingsWatchWithoutAStore(t *testing.T) {
	r := newRig(t)
	r.p.settings.store = nil
	if err := r.p.wire(core.New()); err != nil {
		t.Fatalf("wire: %v", err)
	}
	for _, typ := range r.bus.subscribedTypes() {
		if typ == settings.ChangedEventType {
			t.Errorf("the settings watch was subscribed without a settings store")
		}
	}
}

// TestNoSettingsWatchWithoutABus holds the other half of the guard: with no bus
// the module registers nothing at all.
func TestNoSettingsWatchWithoutABus(t *testing.T) {
	r := newRig(t)
	r.p.bus = nil
	if err := r.p.wire(core.New()); err != nil {
		t.Fatalf("wire: %v", err)
	}
	if got := r.bus.subscribedTypes(); len(got) != 0 {
		t.Errorf("surviving a wire with no bus, the bus carries %v, want none", got)
	}
}

// TestTheHandlerDoesNotPanicWithoutABusOrStore holds the inert case: a product
// with neither a bus nor a store is never subscribed, and the handler invoked on
// its own returns without panicking and without an audit seam to write to.
func TestTheHandlerDoesNotPanicWithoutABusOrStore(t *testing.T) {
	p := &product{logger: (&logBuffer{}).logger()}
	if err := p.onSettingsChanged(context.Background(), event.Event{
		Type:    settings.ChangedEventType,
		Payload: settings.Change{Key: accessTTLPath, Old: "10m0s", New: "20m0s"},
	}); err != nil {
		t.Errorf("onSettingsChanged without a bus or store = %v, want nil", err)
	}
}
