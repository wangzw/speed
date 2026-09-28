package journal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vislake/speed/pkg/event"
)

// change is shaped like the settings module's change payload: untagged fields,
// and the facts that tell one kind of change from another — a delete, a
// withheld value, the version written — carried as a boolean and a number
// rather than as text.
type change struct {
	Key       string
	Old       string
	New       string
	Deleted   bool
	Actor     string
	Version   uint64
	Sensitive bool
}

// publishOne records one event of type "settings.changed" and returns the detail
// of the row it left, decoded.
func publishOne(t *testing.T, payload any) map[string]string {
	t.Helper()
	handle := open(t)
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"settings.changed"}})
	j.subscribe(bus)
	if err := bus.Publish(context.Background(), event.Event{Type: "settings.changed", Payload: payload}); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(rows[0].Detail), &detail); err != nil {
		t.Fatalf("decoding the detail %s: %v", rows[0].Detail, err)
	}
	return detail
}

// A payload's boolean and numeric fields reach the detail, spelled the way the
// payload's own serialization spells them.
//
// Without them a delete reads as a write of the empty value, a change to a
// withheld value reads as a change from nothing to nothing, and the version the
// change produced is gone — and the event path is where the settings module
// leaves the history of its changes.
func TestEventDetailKeepsScalarFields(t *testing.T) {
	t.Run("a delete", func(t *testing.T) {
		detail := publishOne(t, change{Key: "crm.stages", Old: "a", Deleted: true, Actor: "local/alice"})
		for key, want := range map[string]string{
			"Key": "crm.stages", "Old": "a", "New": "", "Actor": "local/alice",
			"Deleted": "true", "Sensitive": "false", "Version": "0",
		} {
			if got, ok := detail[key]; !ok || got != want {
				t.Errorf("detail[%q] is %q (present %v), want %q; detail: %v", key, got, ok, want, detail)
			}
		}
	})
	t.Run("a withheld value", func(t *testing.T) {
		detail := publishOne(t, change{Key: "crm.smtp.password", Sensitive: true, Version: 7})
		if detail["Sensitive"] != "true" || detail["Version"] != "7" || detail["Deleted"] != "false" {
			t.Errorf("the change's markers are missing from the detail: %v", detail)
		}
	})
	t.Run("a number past a float's exact range", func(t *testing.T) {
		detail := publishOne(t, change{Key: "k", Version: 1<<60 + 1})
		if want := "1152921504606846977"; detail["Version"] != want {
			t.Errorf("Version came back as %q, want %q", detail["Version"], want)
		}
	})
}

// Nested values stay out of the detail. Masking compares whole top-level keys,
// so a key inside a nested value is never compared at all, and rendering the
// value as text would put whatever it carries in the table unmasked.
func TestEventDetailLeavesNestedValuesOut(t *testing.T) {
	detail := publishOne(t, map[string]any{
		"object": "user/u1",
		"login":  map[string]any{"password": "hunter2"},
		"tags":   []string{"a", "b"},
		"none":   nil,
	})
	for _, key := range []string{"login", "tags", "none"} {
		if value, present := detail[key]; present {
			t.Errorf("detail[%q] is present as %q, want it left out", key, value)
		}
	}
	if detail["object"] != "user/u1" {
		t.Errorf("the string field was dropped: %v", detail)
	}
}
