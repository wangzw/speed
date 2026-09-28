package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
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

// replicas builds journals over one table, each subscribed on a bus of its own:
// the shape a transport that broadcasts every event gives each replica. Every
// journal reports its failures into the one buffer returned.
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

// deliver hands one event to each bus in turn.
func deliver(t *testing.T, evt event.Event, buses ...*recordingBus) {
	t.Helper()
	for _, bus := range buses {
		if err := bus.Publish(context.Background(), evt); err != nil {
			t.Fatalf("publishing: %v", err)
		}
	}
}

// An event a broadcast bus hands to every replica is recorded once, and the row
// carries the event's identifier.
//
// Every replica runs its own subscription into the one table, so without a key
// the table holds a row per replica — and a row per redelivery — that a reader
// cannot tell from separate events. The identifier on the row is what lets a
// reader match the row to the event it records. A delivery that finds the row
// already written has nothing to report: the fact is in the table.
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

// requireTheFirstDeliveryIsKept delivers one event through a journal on the
// first handle, then again, later and with everything a delivery carries changed
// but the identifier, through a journal on the second, and checks that the
// table still holds the row the first delivery wrote, unchanged.
//
// A later delivery that rewrote the row — an insert that updates on the key
// instead of yielding to it — would leave one row with the right identifier, and
// the count alone would not tell it from the row the first delivery wrote. What
// tells them apart is the row's content: its acceptance time, its tenant and
// what it read from the payload.
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

// sameText reports whether two nullable columns hold the same value.
func sameText(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// text renders a nullable column for a failure message.
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

// Two events are two rows even when everything but the identifier agrees, and
// an event that carries no identifier is recorded on every delivery rather than
// merged with every other such event under an empty key.
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

// requireTheUpgradeKeepsTheTable builds the audit table from the dialect's first
// migration, puts a row in it, applies the rest, and checks that the row is
// still there and that every capture path writes into the table afterwards.
//
// A table built before the event column existed is what a deployment already
// running this module has, and the later migrations have to take it as it is:
// a file rewritten in place would never run there, because the database module
// records the first file as applied.
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
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	requireTheUpgradeKeepsTheTable(t, handle, auditDialect)
}
