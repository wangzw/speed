package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
)

type change struct {
	Key       string
	Old       string
	New       string
	Deleted   bool
	Actor     string
	Version   uint64
	Sensitive bool
}

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

// A payload's boolean and numeric fields reach the detail, spelled the
// way the payload's own serialization spells them.
func TestEventDetailKeepsScalarFields(t *testing.T) {
	t.Run("a delete", func(t *testing.T) {
		detail := publishOne(t, change{Key: "crm.stages", Old: "a", Deleted: true, Actor: "u_alice"})
		for key, want := range map[string]string{
			"Key": "crm.stages", "Old": "a", "New": "", "Actor": "u_alice",
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

// Nested values stay out of the detail.
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

func replicas(t *testing.T, handle *gorm.DB, n int) ([]*recordingBus, *bytes.Buffer) {
	t.Helper()
	var reported bytes.Buffer
	buses := make([]*recordingBus, n)
	for i := range buses {
		buses[i] = newRecordingBus()
		j := newTestJournal(handle, settings{events: []string{"order.updated"}})
		j.signal = &signal{out: &reported}
		j.subscribe(buses[i])
	}
	return buses, &reported
}

func deliver(t *testing.T, evt event.Event, buses ...*recordingBus) {
	t.Helper()
	for _, bus := range buses {
		if err := bus.Publish(context.Background(), evt); err != nil {
			t.Fatalf("publishing: %v", err)
		}
	}
}

// An event a broadcast bus hands to every replica is recorded once, and
// the row carries the event's identifier.
func TestAnEventDeliveredToEveryReplicaIsRecordedOnce(t *testing.T) {
	handle := open(t)
	buses, reported := replicas(t, handle, 2)
	evt := event.Event{
		ID: event.NewID(), Type: "order.updated", TenantID: "acme",
		Payload: map[string]any{"object": "order/o1"},
	}
	deliver(t, evt, buses[0], buses[1], buses[0])

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("one event delivered to two replicas, and once more to one of them, left %d row(s), want 1",
			len(rows))
	}
	if rows[0].EventID == nil || *rows[0].EventID != evt.ID {
		t.Errorf("the row records event %v, want %q", rows[0].EventID, evt.ID)
	}
	if reported.Len() != 0 {
		t.Errorf("a delivery the table already held was reported as a failure: %s", reported.String())
	}
}

func requireTheFirstDeliveryIsKept(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	var reported bytes.Buffer
	buses := [2]*recordingBus{newRecordingBus(), newRecordingBus()}
	for i, handle := range []*gorm.DB{first, second} {
		j := newTestJournal(handle, settings{events: []string{"order.updated"}})
		j.signal = &signal{out: &reported}
		j.subscribe(buses[i])
	}
	id := event.NewID()
	deliver(t, event.Event{
		ID: id, Type: "order.updated", TenantID: "acme",
		Payload: map[string]any{"object": "order/o1", "outcome": "placed", "note": "first"},
	}, buses[0])
	kept := auditRows(t, first)
	if len(kept) != 1 {
		t.Fatalf("the first delivery left %d row(s), want 1", len(kept))
	}
	// The acceptance time is kept to the millisecond or finer on both
	// dialects, so a later delivery that rewrote the row would change it.
	time.Sleep(20 * time.Millisecond)
	deliver(t, event.Event{
		ID: id, Type: "order.updated", TenantID: "globex",
		Payload: map[string]any{"object": "order/o2", "outcome": "shipped", "note": "second"},
	}, buses[1])

	after := auditRows(t, first)
	if len(after) != 1 {
		t.Fatalf("two deliveries of one event left %d row(s), want 1", len(after))
	}
	want, got := kept[0], after[0]
	if got.ID != want.ID || !got.OccurredAt.Equal(want.OccurredAt) || got.Object != want.Object ||
		got.Outcome != want.Outcome || got.Detail != want.Detail || got.Actor != want.Actor ||
		!sameText(got.TenantID, want.TenantID) || !sameText(got.EventID, want.EventID) {
		t.Errorf("the later delivery changed the row: it reads %+v (tenant %v), want the first delivery's %+v "+
			"(tenant %v)", got, text(got.TenantID), want, text(want.TenantID))
	}
	if reported.Len() != 0 {
		t.Errorf("a delivery the table already held was reported as a failure: %s", reported.String())
	}
}

func sameText(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func text(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}

// The row an event leaves is the first delivery's: a later delivery of the same
// event, to another replica, changes nothing in it.
func TestTheFirstDeliveryOfAnEventIsTheOneKept(t *testing.T) {
	handle := open(t)
	requireTheFirstDeliveryIsKept(t, handle, handle)
}

// Two events stay two rows when only their identifiers differ, and an event
// without one is recorded on every delivery.
func TestOnlyAnEventsOwnIdentifierMergesItsDeliveries(t *testing.T) {
	handle := open(t)
	buses, _ := replicas(t, handle, 2)
	same := map[string]any{"object": "order/o1"}
	deliver(t, event.Event{ID: event.NewID(), Type: "order.updated", Payload: same}, buses[0])
	deliver(t, event.Event{ID: event.NewID(), Type: "order.updated", Payload: same}, buses[0])
	deliver(t, event.Event{Type: "order.updated", Payload: same}, buses...)

	rows := auditRows(t, handle)
	if len(rows) != 4 {
		t.Fatalf("two identified events and one unidentified event delivered twice left %d row(s), want 4",
			len(rows))
	}
	for _, r := range rows[2:] {
		if r.EventID != nil {
			t.Errorf("an event without an identifier was recorded under %q, want none", *r.EventID)
		}
	}
}

func requireTheUpgradeKeepsTheTable(t *testing.T, handle *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil || len(entries) < 2 {
		t.Fatalf("listing the %s migrations gave %v (%v), want the first file and a later one", dialect, entries, err)
	}
	applyMigration(t, handle, dialect+"/"+entries[0].Name())
	if err := handle.Exec("INSERT INTO "+tableName+
		" (occurred_at, actor, tenant_id, source, action, object, outcome, purpose, detail)"+
		" VALUES (?, '', NULL, 'seam', 'before', '', '', '', '{}')", time.Now().UTC()).Error; err != nil {
		t.Fatalf("writing a row into the table the first migration built: %v", err)
	}
	for _, entry := range entries[1:] {
		applyMigration(t, handle, dialect+"/"+entry.Name())
	}

	if err := handle.Create(&order{ID: "o1", Note: "after"}).Error; err != nil {
		t.Fatalf("a model write after the upgrade: %v", err)
	}
	buses, reported := replicas(t, handle, 2)
	deliver(t, event.Event{ID: event.NewID(), Type: "order.updated"}, buses...)
	if reported.Len() != 0 {
		t.Errorf("a capture path failed on the upgraded table: %s", reported.String())
	}

	rows := auditRows(t, handle)
	if len(rows) != 3 {
		t.Fatalf("want the row from before the upgrade, the write and the event, got %+v", rows)
	}
	if before := rows[0]; before.Action != "before" || before.EventID != nil {
		t.Errorf("the row written before the upgrade reads back as %+v", before)
	}
	if rows[1].Source != string(audit.SourceStatement) || rows[2].Source != string(audit.SourceEvent) {
		t.Errorf("the rows written after the upgrade are %+v", rows[1:])
	}
}

// The migration adding the event column keeps a table the first one built.
func TestTheEventColumnMigrationKeepsAnExistingTable(t *testing.T) {
	handle := sharedMemory(t)
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	requireTheUpgradeKeepsTheTable(t, handle, auditDialect)
}
