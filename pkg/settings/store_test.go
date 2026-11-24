package settings

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

type demo struct {
	Window   time.Duration
	Limit    int
	Count    uint
	Label    string
	On       bool
	Ratio    float64
	StartsAt time.Time
}

func demoDefaults() demo {
	return demo{Window: time.Minute, Limit: 100, Count: 3, Label: "fallback", Ratio: 0.5}
}

const demoNamespace = "demo"

func demoTable(t *testing.T) *keyTable {
	t.Helper()
	defaults := demoDefaults()
	return tableOf(t, declaring("test", Schema{
		Namespace: demoNamespace,
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"label": {Description: "a label", Sensitive: true},
		},
	}))
}

// A read falls back tenant row, then global row, then the declared default.
func TestReadFallsBackThroughTheThreeLayers(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	b := store

	if got, err := Get[int](b, ForTenant("t1"), "demo.limit"); err != nil || got != 100 {
		t.Fatalf("unset key read %v (%v), want the declared default 100", got, err)
	}

	if err := b.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 200}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}
	for _, tenant := range []string{"t1", "t2"} {
		if got, _ := Get[int](b, ForTenant(tenant), "demo.limit"); got != 200 {
			t.Errorf("tenant %q read %v, want the global row's 200", tenant, got)
		}
	}

	if err := b.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 300}); err != nil {
		t.Fatalf("writing the tenant row: %v", err)
	}
	if got, _ := Get[int](b, ForTenant("t1"), "demo.limit"); got != 300 {
		t.Errorf("t1 read %v, want its own 300", got)
	}
	if got, _ := Get[int](b, ForTenant("t2"), "demo.limit"); got != 200 {
		t.Errorf("t2 read %v, want the global row's 200", got)
	}
}

// TestTheGlobalLayerReadsAndWritesTheGlobalRow checks that the global layer's
// reads and writes land on the row with an empty tenant column.
func TestTheGlobalLayerReadsAndWritesTheGlobalRow(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 42}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}

	var tenantID string
	if err := store.handle.Raw("SELECT tenant_id FROM "+tableName+" WHERE key = ?", "demo.limit").
		Row().Scan(&tenantID); err != nil {
		t.Fatalf("reading the written row's tenant column: %v", err)
	}
	if tenantID != "" {
		t.Errorf("the global write landed on a row whose tenant column is %q, want empty", tenantID)
	}

	if got, _ := Get[int](store, Global(), "demo.limit"); got != 42 {
		t.Errorf("the global layer read %v, want its own 42", got)
	}
	if got, _ := Get[int](store, ForTenant("t1"), "demo.limit"); got != 42 {
		t.Errorf("tenant t1 read %v, want the global row's 42", got)
	}
}

// Deleting a layer's row sends reads back to the layer below it.
func TestDeleteFallsBackToTheLayerBelow(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 200}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}
	if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 300}); err != nil {
		t.Fatalf("writing the tenant row: %v", err)
	}
	if err := store.Delete(ctx, ForTenant("t1"), "demo.limit", "operator"); err != nil {
		t.Fatalf("deleting the tenant row: %v", err)
	}
	if got, _ := Get[int](store, ForTenant("t1"), "demo.limit"); got != 200 {
		t.Errorf("after the delete t1 read %v, want the global row's 200", got)
	}
	if err := store.Delete(ctx, ForTenant("t9"), "demo.limit", "operator"); err != nil {
		t.Errorf("deleting an absent row returned %v, want nil", err)
	}
}

// TestAnUndeclaredKeyIsRefused checks that both directions refuse an undeclared key before reading or writing.
func TestAnUndeclaredKeyIsRefused(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	var target int
	if err := store.Get(ForTenant("t1"), "demo.missing", &target); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("reading an undeclared key returned %v, want ErrUnknownKey", err)
	}
	err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.missing", Value: 1})
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("writing an undeclared key returned %v, want ErrUnknownKey", err)
	}
}

// TestReadingIntoTheWrongTypeIsAMismatch checks that a wrong target type is a mismatch rather than a panic.
func TestReadingIntoTheWrongTypeIsAMismatch(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	var target string
	if err := store.Get(ForTenant("t1"), "demo.limit", &target); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("reading an int key into a string returned %v, want ErrTypeMismatch", err)
	}
}

// TestTheGenericReadReportsAMismatch checks that the generic wrapper reports the same mismatch.
func TestTheGenericReadReportsAMismatch(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	if _, err := Get[string](store, ForTenant("t1"), "demo.limit"); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("Get[string] on an int key returned %v, want ErrTypeMismatch", err)
	}
}

// TestABadTargetPanics checks that a target that is not a non-nil pointer panics.
func TestABadTargetPanics(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	for _, c := range []struct {
		name   string
		target any
	}{
		{"nil", nil},
		{"a value", 0},
		{"a nil pointer", (*int)(nil)},
	} {
		for _, key := range []string{"demo.limit", "demo.missing"} {
			t.Run(c.name+"/"+key, func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Errorf("Get of %s with %s as its target did not panic", key, c.name)
					}
				}()
				_ = store.Get(ForTenant("t1"), key, c.target)
			})
		}
	}
}

// TestAZeroLayerPanics checks that a zero layer panics rather than returning an error, the judgement a bad target gets.
func TestAZeroLayerPanics(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	var target int
	cases := []struct {
		name string
		call func()
	}{
		{"Get", func() { _ = store.Get(nil, "demo.limit", &target) }},
		{"the generic read", func() { _, _ = Get[int](store, nil, "demo.limit") }},
		{"WrittenAt", func() { _, _, _ = store.WrittenAt(nil, "demo.limit") }},
		{"the helper revision read", func() { _, _, _ = WrittenAt(store, nil, "demo.limit") }},
		{"Set", func() { _ = store.Set(ctx, Write{Key: "demo.limit", Value: 1}) }},
		{"Delete", func() { _ = store.Delete(ctx, nil, "demo.limit", "operator") }},
		{"List", func() { _, _ = store.List(ctx, nil) }},
		// The mistake is the call site's whatever the rest of the call holds, so the
		// panic must not wait for the key or value to pass.
		{"Get of an undeclared key", func() { _ = store.Get(nil, "demo.missing", &target) }},
		{"WrittenAt of an undeclared key", func() { _, _, _ = store.WrittenAt(nil, "demo.missing") }},
		{"Set of an undeclared key", func() { _ = store.Set(ctx, Write{Key: "demo.missing", Value: 1}) }},
		{"Set of a value that does not convert", func() {
			_ = store.Set(ctx, Write{Key: "demo.limit", Value: "not a number"})
		}},
		{"Set of an oversized value", func() {
			_ = store.Set(ctx, Write{Key: "demo.label", Value: strings.Repeat("x", maxValueBytes+1)})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("%s with a zero layer did not panic", c.name)
				}
			}()
			c.call()
		})
	}
}

// TestForTenantRejectsAnEmptyIdentifier checks that an empty identifier panics at the constructor.
func TestForTenantRejectsAnEmptyIdentifier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error(`ForTenant("") did not panic`)
		}
	}()
	_ = ForTenant("")
}

// TestForTenantRejectsAnIdentifierOutsideTheDomain checks the whole domain: an
// id outside it names no tenant and is refused at the constructor.
func TestForTenantRejectsAnIdentifierOutsideTheDomain(t *testing.T) {
	for _, id := range []string{"", " ", "\t", " \t ", "acme ", " acme", "a b", "a\nb", "a\x00b"} {
		t.Run(strconv.Quote(id), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("ForTenant(%q) did not panic", id)
				}
			}()
			_ = ForTenant(id)
		})
	}
	for _, id := range []string{"acme", "acme-1", "a.b", strings.Repeat("a", 200)} {
		t.Run("accepted/"+id, func(t *testing.T) {
			if got := ForTenant(id); got == nil {
				t.Errorf("ForTenant(%q) = nil, want a layer", id)
			}
		})
	}
}

// TestWritingAValueOfTheWrongTypeIsAMismatch checks that a wrongly typed write is refused.
func TestWritingAValueOfTheWrongTypeIsAMismatch(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: "not a number"})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("writing a string to an int key returned %v, want ErrTypeMismatch", err)
	}
}

// TestABrokenRowReportsItselfAndSparesTheRest checks that a row that does not
// decode is reported on read while every other key keeps working.
func TestABrokenRowReportsItselfAndSparesTheRest(t *testing.T) {
	keys := demoTable(t)
	handle := open(t)
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('t1', 'demo.limit', 'not-a-number', 1, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a bad row: %v", err)
	}
	store := newSettingsStore(keys, stubDatabase{handle: handle, dialect: demoDialect()}, newRecordingBus(), moduleSettings{})
	store.logger = discardLogger()
	if err := store.load(context.Background()); err != nil {
		t.Fatalf("loading with one bad row returned %v, want nil", err)
	}

	var target int
	err := store.Get(ForTenant("t1"), "demo.limit", &target)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("reading the bad row returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "demo.limit") {
		t.Errorf("the mismatch does not name the key: %v", err)
	}
	if got, err := Get[int](store, ForTenant("t2"), "demo.limit"); err != nil || got != 100 {
		t.Errorf("an unrelated read returned %v (%v), want the declared default", got, err)
	}
}

// TestAnOverwrittenBrokenRowReportsWhatWasThere checks that a write replacing a broken row reports the text that was there.
func TestAnOverwrittenBrokenRowReportsWhatWasThere(t *testing.T) {
	keys := demoTable(t)
	handle := open(t)
	const planted = "not-a-number"
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'demo.limit', ?, 1, ?)", planted, time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a bad row: %v", err)
	}
	bus := newRecordingBus()
	store := newSettingsStore(keys, stubDatabase{handle: handle, dialect: demoDialect()}, bus, moduleSettings{})
	store.logger = discardLogger()
	if err := store.load(context.Background()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 5}); err != nil {
		t.Fatalf("overwriting the broken row: %v", err)
	}
	events := bus.events()
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	change := events[0].Payload.(Change)
	if change.Old != planted {
		t.Errorf("the change reports the old value as %q, want the row that was there (%q)",
			change.Old, planted)
	}
	if change.New != "5" {
		t.Errorf("the change reports the new value as %q, want 5", change.New)
	}
}

// TestWriteIncrementsTheVersionAndPublishes checks the incremented version and the value the event carries.
func TestWriteIncrementsTheVersionAndPublishes(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	ctx := context.Background()

	if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 7, Actor: "alice"}); err != nil {
		t.Fatalf("the first write: %v", err)
	}
	if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 8, Actor: "bob"}); err != nil {
		t.Fatalf("the second write: %v", err)
	}

	events := bus.events()
	if len(events) != 2 {
		t.Fatalf("want two events, got %d", len(events))
	}
	if events[0].Type != ChangedEventType {
		t.Errorf("the event type is %q, want %q", events[0].Type, ChangedEventType)
	}
	if events[0].TenantID != "t1" {
		t.Errorf("the envelope's tenant is %q, want t1", events[0].TenantID)
	}
	first := events[0].Payload.(Change)
	if first.Old != "" || first.New != "7" || first.Version != 1 || first.Actor != "alice" {
		t.Errorf("the first change is %+v, want Old empty, New 7, version 1, actor alice", first)
	}
	second := events[1].Payload.(Change)
	if second.Old != "7" || second.New != "8" || second.Version != 2 {
		t.Errorf("the second change is %+v, want Old 7, New 8, version 2", second)
	}
}

// TestASensitiveValueNeverReachesTheEvent checks that a sensitive change carries the fact and not the value.
func TestASensitiveValueNeverReachesTheEvent(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.label", Value: "hunter2"}); err != nil {
		t.Fatalf("writing the sensitive item: %v", err)
	}
	events := bus.events()
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	change := events[0].Payload.(Change)
	if !change.Sensitive {
		t.Error("the change does not carry the sensitive marker")
	}
	if change.Old != "" || change.New != "" {
		t.Errorf("a sensitive change carries values: Old %q, New %q", change.Old, change.New)
	}
	if change.Key != "demo.label" {
		t.Errorf("the change names %q, want the key", change.Key)
	}
}

// TestAGlobalChangeCarriesNoTenant checks that a global change carries no tenant on the envelope.
func TestAGlobalChangeCarriesNoTenant(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 5}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}
	if got := bus.events()[0].TenantID; got != "" {
		t.Errorf("the envelope's tenant is %q, want empty for the global row", got)
	}
}

// TestDeletePublishesTheOldValue checks that a delete carries the old value.
func TestDeletePublishesTheOldValue(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 9}); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := store.Delete(ctx, ForTenant("t1"), "demo.limit", "carol"); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	events := bus.events()
	change := events[len(events)-1].Payload.(Change)
	if !change.Deleted || change.Old != "9" || change.New != "" || change.Actor != "carol" {
		t.Errorf("the delete change is %+v, want Deleted, Old 9, empty New, actor carol", change)
	}
}

// TestAnOversizedValueIsRefused checks that a value over the limit is refused before anything is written.
func TestAnOversizedValueIsRefused(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	huge := strings.Repeat("x", maxValueBytes+1)
	err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.label", Value: huge})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("writing an oversized value returned %v, want ErrInvalidValue", err)
	}
	if len(bus.events()) != 0 {
		t.Error("a refused write still published an event")
	}
	if rows := countRows(t, store); rows != 0 {
		t.Errorf("a refused write left %d rows behind", rows)
	}
}

// TestAValueOfExactlyTheLimitIsAdmitted checks that a value of exactly the limit is admitted.
func TestAValueOfExactlyTheLimitIsAdmitted(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	exact := strings.Repeat("x", maxValueBytes)
	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.label", Value: exact}); err != nil {
		t.Fatalf("writing a value of exactly the limit returned %v, want nil", err)
	}
}

// TestDeleteClearsARowWhoseDeclarationIsGone checks that Delete clears a row whose declaration is gone.
func TestDeleteClearsARowWhoseDeclarationIsGone(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, handle := storeOf(t, keys, bus)
	ctx := context.Background()
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('t1', 'gone.key', 'x', 1, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a row for an undeclared key: %v", err)
	}

	if err := store.Delete(ctx, ForTenant("t1"), "gone.key", "operator"); err != nil {
		t.Fatalf("deleting a row with no declaration returned %v, want nil", err)
	}
	rows, err := store.List(ctx, ForTenant("t1"))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("the row is still there: %+v", rows)
	}
}

// TestADeleteReportsWhatTheRemovedRowHeld checks that a delete reports the value
// read back by the deleting statement, not the snapshot.
func TestADeleteReportsWhatTheRemovedRowHeld(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"a row whose declaration is gone", "gone.key"},
		{"a row another replica wrote", "demo.limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := demoTable(t)
			bus := newRecordingBus()
			store, handle := storeOf(t, keys, bus)
			if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
				"VALUES ('t1', ?, '41', 1, ?)", c.key, time.Now().UTC()).Error; err != nil {
				t.Fatalf("planting a row: %v", err)
			}
			if err := store.Delete(context.Background(), ForTenant("t1"), c.key, "operator"); err != nil {
				t.Fatalf("deleting: %v", err)
			}
			events := bus.events()
			if len(events) != 1 {
				t.Fatalf("want one event for the removed row, got %d", len(events))
			}
			change := events[0].Payload.(Change)
			if !change.Deleted || change.Old != "41" {
				t.Errorf("the change is %+v, want a delete whose Old is the row's 41", change)
			}
		})
	}
}

// TestADeleteThatRemovedNothingPublishesNothing checks that a delete that removed no row publishes nothing and is not an error.
func TestADeleteThatRemovedNothingPublishesNothing(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	if err := store.Delete(context.Background(), ForTenant("t1"), "demo.limit", "operator"); err != nil {
		t.Fatalf("deleting an absent row returned %v, want nil", err)
	}
	if events := bus.events(); len(events) != 0 {
		t.Errorf("a delete that removed nothing published %+v", events)
	}
}

// TestDeletingASensitiveItemWithholdsItsValue checks that a sensitive delete withholds the value.
func TestDeletingASensitiveItemWithholdsItsValue(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, _ := storeOf(t, keys, bus)
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.label", Value: "hunter2"}); err != nil {
		t.Fatalf("writing the sensitive item: %v", err)
	}
	if err := store.Delete(ctx, Global(), "demo.label", "carol"); err != nil {
		t.Fatalf("deleting the sensitive item: %v", err)
	}

	events := bus.events()
	change := events[len(events)-1].Payload.(Change)
	if !change.Deleted {
		t.Error("the delete change does not report that it was a delete")
	}
	if !change.Sensitive {
		t.Error("the delete change does not carry the sensitive marker")
	}
	if change.Old != "" || change.New != "" {
		t.Errorf("a sensitive delete carries values: Old %q, New %q", change.Old, change.New)
	}
}

// TestListReportsWrittenRowsInKeyOrder checks that List reports written rows in key order with nothing falling back.
func TestListReportsWrittenRowsInKeyOrder(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	for _, w := range []Write{
		{Layer: Global(), Key: "demo.limit", Value: 1},
		{Layer: Global(), Key: "demo.on", Value: true},
		{Layer: ForTenant("t1"), Key: "demo.window", Value: 2 * time.Minute},
	} {
		if err := store.Set(ctx, w); err != nil {
			t.Fatalf("writing %+v: %v", w, err)
		}
	}
	global, err := store.List(ctx, Global())
	if err != nil {
		t.Fatalf("listing the global rows: %v", err)
	}
	if len(global) != 2 || global[0].Key != "demo.limit" || global[1].Key != "demo.on" {
		t.Fatalf("the global rows are %+v, want demo.limit then demo.on", global)
	}
	if global[1].Value != "true" {
		t.Errorf("the boolean row holds %q, want true", global[1].Value)
	}
	if global[0].UpdatedAt.IsZero() {
		t.Error("the listed row carries no timestamp")
	}
	if global[0].UpdatedAt.After(time.Now().UTC()) {
		t.Errorf("the listed row's timestamp %v is in the future", global[0].UpdatedAt)
	}
	if global[0].Version != 1 {
		t.Errorf("the listed row's version is %d, want 1 for its first write", global[0].Version)
	}
	tenant, err := store.List(ctx, ForTenant("t1"))
	if err != nil {
		t.Fatalf("listing t1's rows: %v", err)
	}
	if len(tenant) != 1 || tenant[0].Key != "demo.window" || tenant[0].Value != "2m0s" {
		t.Errorf("t1's rows are %+v, want one demo.window row reading 2m0s", tenant)
	}
}

// TestValuesRoundTrip checks that every arm of the closed type set round-trips, written and read back.
func TestValuesRoundTrip(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	starts := time.Date(2026, 9, 21, 12, 30, 0, 0, time.UTC)
	writes := []Write{
		{Layer: Global(), Key: "demo.window", Value: 90 * time.Second},
		{Layer: Global(), Key: "demo.limit", Value: -12},
		{Layer: Global(), Key: "demo.count", Value: uint(7)},
		{Layer: Global(), Key: "demo.label", Value: "a label"},
		{Layer: Global(), Key: "demo.on", Value: true},
		{Layer: Global(), Key: "demo.ratio", Value: 0.125},
		{Layer: Global(), Key: "demo.starts-at", Value: starts},
	}
	for _, w := range writes {
		if err := store.Set(ctx, w); err != nil {
			t.Fatalf("writing %s: %v", w.Key, err)
		}
	}
	if got, err := Get[time.Duration](store, Global(), "demo.window"); err != nil || got != 90*time.Second {
		t.Errorf("the duration read back as %v (%v)", got, err)
	}
	if got, _ := Get[int](store, Global(), "demo.limit"); got != -12 {
		t.Errorf("the integer read back as %v", got)
	}
	if got, _ := Get[uint](store, Global(), "demo.count"); got != 7 {
		t.Errorf("the unsigned integer read back as %v", got)
	}
	if got, _ := Get[bool](store, Global(), "demo.on"); got != true {
		t.Errorf("the boolean read back as %v", got)
	}
	if got, _ := Get[string](store, Global(), "demo.label"); got != "a label" {
		t.Errorf("the string read back as %q", got)
	}
	if got, _ := Get[float64](store, Global(), "demo.ratio"); got != 0.125 {
		t.Errorf("the float read back as %v", got)
	}
	got, err := Get[time.Time](store, Global(), "demo.starts-at")
	if err != nil {
		t.Fatalf("reading the timestamp: %v", err)
	}
	if !got.Equal(starts) {
		t.Errorf("the timestamp read back as %v, want %v", got, starts)
	}
}

// TestADeliveredEventReloadsTheSnapshot checks that a delivered change reloads the snapshot.
func TestADeliveredEventReloadsTheSnapshot(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, handle := storeOf(t, keys, bus)
	store.subscribe()

	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'demo.limit', '777', 1, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a row: %v", err)
	}
	if got, _ := Get[int](store, Global(), "demo.limit"); got != 100 {
		t.Fatalf("before the event the read was %v, want the stale default", got)
	}
	bus.deliver(t, ChangedEventType, eventOf("demo.limit"))
	if got, _ := Get[int](store, Global(), "demo.limit"); got != 777 {
		t.Errorf("after the event the read was %v, want 777", got)
	}
}

// TestAWriteIsVisibleToTheWriterImmediately checks that a write reloads its own snapshot at once.
func TestAWriteIsVisibleToTheWriterImmediately(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	if err := store.Set(context.Background(), Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 5}); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if got, _ := Get[int](store, ForTenant("t1"), "demo.limit"); got != 5 {
		t.Errorf("the writer read %v, want its own 5", got)
	}
}

// TestWrittenAtReportsTheRowsWriteTime checks that a written row answers the time the table holds.
func TestWrittenAtReportsTheRowsWriteTime(t *testing.T) {
	keys := demoTable(t)
	store, handle := storeOf(t, keys, newRecordingBus())
	if err := store.Set(context.Background(), Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 5}); err != nil {
		t.Fatalf("writing: %v", err)
	}

	at, present, err := store.WrittenAt(ForTenant("t1"), "demo.limit")
	if err != nil {
		t.Fatalf("reading the revision: %v", err)
	}
	if !present {
		t.Fatal("the layer holds the row, and the read reported it absent")
	}
	var stored time.Time
	if err := handle.Raw("SELECT updated_at FROM "+tableName+" WHERE tenant_id = ? AND key = ?",
		"t1", "demo.limit").Row().Scan(&stored); err != nil {
		t.Fatalf("reading the row's column: %v", err)
	}
	if !at.Equal(stored) {
		t.Errorf("the revision is %v, and the row's updated_at is %v", at, stored)
	}

	if err := handle.Exec("DROP TABLE " + tableName).Error; err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	if again, stillPresent, err := store.WrittenAt(ForTenant("t1"), "demo.limit"); err != nil ||
		!stillPresent || !again.Equal(at) {
		t.Errorf("with the table gone the revision is %v, present=%v, err=%v; want %v",
			again, stillPresent, err, at)
	}
}

// TestWrittenAtDoesNotFallBack checks that the revision read does not fall back.
func TestWrittenAtDoesNotFallBack(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 200}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}
	if got, _ := Get[int](store, ForTenant("t1"), "demo.limit"); got != 200 {
		t.Fatalf("the value read is %v, so the case is not testing the fallback", got)
	}

	at, present, err := store.WrittenAt(ForTenant("t1"), "demo.limit")
	if err != nil {
		t.Fatalf("reading a tenant revision: %v", err)
	}
	if present {
		t.Errorf("tenant t1 holds no row of its own, and the read reported one written at %v", at)
	}
	if !at.IsZero() {
		t.Errorf("an absent revision carries %v, want the zero time", at)
	}

	if _, present, err := store.WrittenAt(Global(), "demo.limit"); err != nil || !present {
		t.Errorf("the global layer reported present=%v, err=%v, want its row", present, err)
	}
}

// TestReadAnswersEveryLayerWithItsOwnRow checks that Read answers each layer with its own value and row's write time.
func TestReadAnswersEveryLayerWithItsOwnRow(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 200}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}
	if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 300}); err != nil {
		t.Fatalf("writing the tenant row: %v", err)
	}

	values, err := ReadLayers[int](store, "demo.limit", Global(), ForTenant("t1"), ForTenant("t2"))
	if err != nil {
		t.Fatalf("reading the layers: %v", err)
	}
	if values[0].Value != 200 || !values[0].Present || values[0].WrittenAt.IsZero() {
		t.Errorf("the global layer answered %+v, want its own row's 200", values[0])
	}
	if values[1].Value != 300 || !values[1].Present || values[1].WrittenAt.IsZero() {
		t.Errorf("tenant t1 answered %+v, want its own row's 300", values[1])
	}
	if values[2].Value != 100 || values[2].Present || !values[2].WrittenAt.IsZero() {
		t.Errorf("tenant t2 answered %+v, want the declared default with no row", values[2])
	}

	if got, _ := Get[int](store, ForTenant("t2"), "demo.limit"); got != 200 {
		t.Fatalf("the fallback read is %v, so the case is not testing the absence", got)
	}
}

// TestReadJudgesItsArgumentsAsTheOtherReadsDo checks that Read judges its arguments as Get and WrittenAt do.
func TestReadJudgesItsArgumentsAsTheOtherReadsDo(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	var target int

	if _, err := (&settingsStore{keys: keys}).Read("demo.limit",
		LayerRead{Layer: Global(), Target: &target}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("a read before the snapshot loaded gave %v, want ErrNotLoaded", err)
	}
	if _, err := store.Read("demo.missing",
		LayerRead{Layer: Global(), Target: &target}); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("a read of an undeclared key gave %v, want ErrUnknownKey", err)
	}
	for name, call := range map[string]func(){
		"a bad target": func() { _, _ = store.Read("demo.limit", LayerRead{Layer: Global(), Target: 0}) },
		"a zero layer": func() { _, _ = store.Read("demo.limit", LayerRead{Layer: nil, Target: &target}) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Read with %s did not panic", name)
				}
			}()
			call()
		})
	}
}

// TestWrittenAtMovesAfterASetAndSurvivesAReload checks that a write moves the revision and a reload keeps the row's own time.
func TestWrittenAtMovesAfterASetAndSurvivesAReload(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 1}); err != nil {
		t.Fatalf("the first write: %v", err)
	}
	first, present, err := store.WrittenAt(ForTenant("t1"), "demo.limit")
	if err != nil || !present {
		t.Fatalf("the first revision: present=%v, err=%v", present, err)
	}

	if err = store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 2}); err != nil {
		t.Fatalf("the second write: %v", err)
	}
	second, _, err := store.WrittenAt(ForTenant("t1"), "demo.limit")
	if err != nil {
		t.Fatalf("the second revision: %v", err)
	}
	if !second.After(first) {
		t.Errorf("the revision after a second write is %v, and the first was %v; it did not move",
			second, first)
	}

	store.reload(ctx)
	after, present, err := store.WrittenAt(ForTenant("t1"), "demo.limit")
	if err != nil || !present {
		t.Fatalf("the revision after a reload: present=%v, err=%v", present, err)
	}
	if !after.Equal(second) {
		t.Errorf("the reload brought the revision %v, want the row's own %v", after, second)
	}
}

// TestAReloadedRevisionIsTheRowsOwnTime checks that a reloaded revision is the column's value.
func TestAReloadedRevisionIsTheRowsOwnTime(t *testing.T) {
	keys := demoTable(t)
	store, handle := storeOf(t, keys, newRecordingBus())
	written := time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('t1', 'demo.limit', '7', 1, ?)", written).Error; err != nil {
		t.Fatalf("planting a row: %v", err)
	}
	store.reload(context.Background())

	at, present, err := store.WrittenAt(ForTenant("t1"), "demo.limit")
	if err != nil || !present {
		t.Fatalf("the revision: present=%v, err=%v", present, err)
	}
	if !at.Equal(written) {
		t.Errorf("the revision is %v, want the row's own %v", at, written)
	}
}

// TestWrittenAtJudgesItsArgumentsAsGetDoes checks that the revision read judges
// its arguments as Get does and a broken row still answers its time.
func TestWrittenAtJudgesItsArgumentsAsGetDoes(t *testing.T) {
	keys := demoTable(t)
	if _, _, err := (&settingsStore{keys: keys}).WrittenAt(ForTenant("t1"), "demo.limit"); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("a read before the load returned %v, want ErrNotLoaded", err)
	}

	store, handle := storeOf(t, keys, newRecordingBus())
	if _, _, err := store.WrittenAt(ForTenant("t1"), "demo.missing"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("an undeclared key returned %v, want ErrUnknownKey", err)
	}
	if _, present, err := WrittenAt(store, ForTenant("t1"), "demo.limit"); err != nil || present {
		t.Errorf("the helper answered present=%v, err=%v, want absent and no error", present, err)
	}

	written := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('t2', 'demo.limit', 'not-a-number', 1, ?)", written).Error; err != nil {
		t.Fatalf("planting a bad row: %v", err)
	}
	store.reload(context.Background())
	at, present, err := store.WrittenAt(ForTenant("t2"), "demo.limit")
	if err != nil || !present {
		t.Fatalf("the broken row's revision: present=%v, err=%v", present, err)
	}
	if !at.Equal(written) {
		t.Errorf("the broken row's revision is %v, want its write time %v", at, written)
	}
	var target int
	if err := store.Get(ForTenant("t2"), "demo.limit", &target); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("reading the broken value returned %v, want ErrTypeMismatch", err)
	}
}

func cancelWhenReadingBack(t *testing.T, handle *gorm.DB, cancel context.CancelFunc) {
	t.Helper()
	err := handle.Callback().Row().Before("gorm:row").Register("test:cancel-when-reading-back",
		func(tx *gorm.DB) {
			if strings.HasPrefix(tx.Statement.SQL.String(), "SELECT tenant_id, key, value, updated_at FROM "+tableName) {
				cancel()
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
}

// TestAWriteThatLandedFinishesAfterTheCallerLeaves checks that a landed write
// reloads and publishes whatever becomes of the caller's context.
func TestAWriteThatLandedFinishesAfterTheCallerLeaves(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, handle := storeOf(t, keys, bus)
	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 12}); err != nil {
		t.Fatalf("writing the first value: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelWhenReadingBack(t, handle, cancel)

	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 99}); err != nil {
		t.Fatalf("the write returned %v, and its statement had committed", err)
	}
	if ctx.Err() == nil {
		t.Fatal("the caller's context was never cancelled, so this case tested nothing")
	}
	if got, _ := Get[int](store, Global(), "demo.limit"); got != 99 {
		t.Errorf("the writer reads %v after its own write, want 99", got)
	}
	if events := bus.events(); len(events) != 2 || events[1].Payload.(Change).New != "99" {
		t.Errorf("published %+v, want the second write's change as well", events)
	}

	deleting, stop := context.WithCancel(context.Background())
	defer stop()
	cancelOnDelete := func(tx *gorm.DB) {
		if strings.HasPrefix(tx.Statement.SQL.String(), "SELECT tenant_id, key, value, updated_at FROM "+tableName) {
			stop()
		}
	}
	if err := handle.Callback().Row().Replace("test:cancel-when-reading-back", cancelOnDelete); err != nil {
		t.Fatalf("replacing the callback: %v", err)
	}
	if err := store.Delete(deleting, Global(), "demo.limit", "operator"); err != nil {
		t.Fatalf("the delete returned %v, and its statement had committed", err)
	}
	if deleting.Err() == nil {
		t.Fatal("the deleting context was never cancelled, so this half tested nothing")
	}
	if got, _ := Get[int](store, Global(), "demo.limit"); got != 100 {
		t.Errorf("the writer reads %v after its delete, want the declared default 100", got)
	}
	if events := bus.events(); len(events) != 3 || !events[2].Payload.(Change).Deleted {
		t.Errorf("published %+v, want the delete's change as well", events)
	}
}

// TestAPublishFailureDoesNotFailTheWrite checks that a publish failure does not undo the write.
func TestAPublishFailureDoesNotFailTheWrite(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	bus.failWith = errors.New("the bus is unreachable")
	store, _ := storeOf(t, keys, bus)
	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 3}); err != nil {
		t.Fatalf("the write returned %v, want nil despite the publish failure", err)
	}
	if got, _ := Get[int](store, Global(), "demo.limit"); got != 3 {
		t.Errorf("the value read back as %v, want 3", got)
	}
}

// TestAMissingTableIsReportedAsSuch checks that a load against a database without the table names it and says what to do.
func TestAMissingTableIsReportedAsSuch(t *testing.T) {
	handle := openMemory(t)
	keys := demoTable(t)
	_, loadErr := loadSnapshot(context.Background(), handle, keys)
	if !errors.Is(loadErr, ErrTableMissing) {
		t.Fatalf("loading without the table returned %v, want ErrTableMissing", loadErr)
	}
	if !strings.Contains(loadErr.Error(), tableName) {
		t.Errorf("the error does not name the table %q: %v", tableName, loadErr)
	}
}

// TestAListFailureNamesItsLayer checks that a List failure names the layer once.
func TestAListFailureNamesItsLayer(t *testing.T) {
	keys := demoTable(t)
	store, handle := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := handle.Exec("DROP TABLE " + tableName).Error; err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	cases := []struct {
		name  string
		layer Layer
		want  string
	}{
		{"the global layer", Global(), "listing the rows of the global layer"},
		{"a tenant layer", ForTenant("t1"), `listing the rows of tenant "t1"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := store.List(ctx, c.layer)
			if err == nil {
				t.Fatal("listing without the table returned nil, want an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the failure does not name the layer once: %v", err)
			}
		})
	}
}

// TestTheAbsentTableJudgement checks the per-engine texts of the absent-table judgement where nothing has to be running.
func TestTheAbsentTableJudgement(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"sqlite", "no such table: settings", true},
		{"postgres", `ERROR: relation "settings" does not exist (SQLSTATE 42P01)`, true},
		{"a connection failure", "dial tcp 127.0.0.1:5432: connect: connection refused", false},
		{"a permission failure", "permission denied for table settings", false},
		{"another table missing", "no such table: tenants", false},
	}
	for _, c := range cases {
		if got := tableMissing(errors.New(c.text), tableName); got != c.want {
			t.Errorf("%s: tableMissing(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
	if tableMissing(nil, tableName) {
		t.Error("tableMissing(nil) reported a failure")
	}
}

func eventOf(key string) event.Event {
	return event.Event{Type: ChangedEventType, Payload: Change{Key: key}}
}

func demoDialect() db.Dialect { return db.SQLite }

func countRows(t *testing.T, store *settingsStore) int {
	t.Helper()
	var n int
	if err := store.handle.Raw("SELECT count(*) FROM " + tableName).Row().Scan(&n); err != nil {
		t.Fatalf("counting the rows: %v", err)
	}
	return n
}

type collectingHandler struct{ lines *[]string }

func (h collectingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h collectingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h collectingHandler) WithGroup(string) slog.Handler            { return h }

func (h collectingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.lines = append(*h.lines, r.Message)
	return nil
}

// TestReplacingTheSnapshotIsRecorded checks that the moment a snapshot takes effect is written down.
func TestReplacingTheSnapshotIsRecorded(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())

	var lines []string
	store.logger = slog.New(collectingHandler{lines: &lines})

	if err := store.Set(context.Background(), Write{
		Layer: Global(),
		Key:   "demo.limit",
		Value: 7,
		Actor: "t",
	}); err != nil {
		t.Fatalf("writing a setting: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("a snapshot took effect and left no record of when it did")
	}

	lines = nil
	store.reload(context.Background())
	if len(lines) != 0 {
		t.Fatalf("a reload that changed nothing was recorded: %v", lines)
	}
}
