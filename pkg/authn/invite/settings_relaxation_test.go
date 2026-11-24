package invite

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

const (
	testRelaxedAction  = "authn.settings.relaxed"
	testRelaxedOutcome = "relaxed"
	testIncrease       = "increase"
)

// TestALongerTTLWritesAWarnAndAnAuditRecord checks that a longer lifetime writes
// one Warn naming key and direction without the value, and one relaxed audit row.
func TestALongerTTLWritesAWarnAndAnAuditRecord(t *testing.T) {
	h := newSettingsAssembly(t)
	h.bus.deliver(t, event.Event{
		Type: settings.ChangedEventType,
		Payload: settings.Change{
			Key: testTTLKey, Old: "168h0m0s", New: "720h0m0s", Actor: "op",
		},
	})

	logs := h.logs.String()
	if n := strings.Count(logs, "level=WARN"); n != 1 {
		t.Fatalf("the log holds %d Warn lines, want 1:\n%s", n, logs)
	}
	lines := h.logs.lines("level=WARN", "setting="+testTTLKey)
	if len(lines) != 1 {
		t.Fatalf("the Warn line does not name the setting %s:\n%s", testTTLKey, logs)
	}
	if !strings.Contains(lines[0], "direction="+testIncrease) {
		t.Errorf("the Warn line does not carry the direction %s:\n%s", testIncrease, lines[0])
	}
	if strings.Contains(lines[0], "720h") {
		t.Errorf("the Warn line names the value:\n%s", lines[0])
	}

	entries := h.audit.entries()
	if len(entries) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(entries))
	}
	if entries[0].Action != testRelaxedAction || entries[0].Object != testTTLKey || entries[0].Outcome != testRelaxedOutcome {
		t.Errorf("audit row = %+v, want the relaxed action on %s", entries[0], testTTLKey)
	}
	if got := entries[0].Detail["change"]; got != testIncrease {
		t.Errorf("the audit detail change = %q, want %q", got, testIncrease)
	}
}
