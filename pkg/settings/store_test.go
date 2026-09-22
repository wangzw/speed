package settings

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// demo is the declaration the read and write cases are held to. Its field
// values are the declared defaults.
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

	// Nothing written: the declared default.
	if got, err := Get[int](b, ForTenant("t1"), "demo.limit"); err != nil || got != 100 {
		t.Fatalf("unset key read %v (%v), want the declared default 100", got, err)
	}

	// A global row covers every tenant that has no row of its own.
	if err := b.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 200}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}
	for _, tenant := range []string{"t1", "t2"} {
		if got, _ := Get[int](b, ForTenant(tenant), "demo.limit"); got != 200 {
			t.Errorf("tenant %q read %v, want the global row's 200", tenant, got)
		}
	}

	// A tenant row wins over the global row, and leaves the other tenants on it.
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

// The global layer's reads and writes land on the global row: the row whose
// tenant column is empty. The column is how storage spells the layer, while
// the interface names it with Global().
func TestTheGlobalLayerReadsAndWritesTheGlobalRow(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 42}); err != nil {
		t.Fatalf("writing the global row: %v", err)
	}

	// The row it landed on is the one with an empty tenant column, rather than
	// one under a placeholder identifier. The read is of the table itself, so
	// that this is not the layer's own filter answering for it.
	var tenantID string
	if err := store.handle.Raw("SELECT tenant_id FROM "+tableName+" WHERE key = ?", "demo.limit").
		Row().Scan(&tenantID); err != nil {
		t.Fatalf("reading the written row's tenant column: %v", err)
	}
	if tenantID != "" {
		t.Errorf("the global write landed on a row whose tenant column is %q, want empty", tenantID)
	}

	// The global layer reads that row, and it is the layer a tenant with no row
	// of its own falls back to.
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
	// Deleting a row that is not there is not an error: the row is gone, which
	// is what the caller asked for.
	if err := store.Delete(ctx, ForTenant("t9"), "demo.limit", "operator"); err != nil {
		t.Errorf("deleting an absent row returned %v, want nil", err)
	}
}

// A key with no declaration is refused by both directions, before anything is
// read or written.
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

// A target of the wrong type is a mismatch rather than a panic: the caller
// asked for a key under a type its declaration does not have.
func TestReadingIntoTheWrongTypeIsAMismatch(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	var target string
	if err := store.Get(ForTenant("t1"), "demo.limit", &target); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("reading an int key into a string returned %v, want ErrTypeMismatch", err)
	}
}

// The generic wrapper carries the same judgement: a type parameter that does
// not match the declaration is refused rather than converted.
func TestTheGenericReadReportsAMismatch(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	if _, err := Get[string](store, ForTenant("t1"), "demo.limit"); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("Get[string] on an int key returned %v, want ErrTypeMismatch", err)
	}
}

// A target that is not a non-nil pointer is a mistake at the call site, and it
// panics rather than returning an error, the way configuration's value-reading
// side judges it.
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
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Get with %s as its target did not panic", c.name)
				}
			}()
			_ = store.Get(ForTenant("t1"), "demo.limit", c.target)
		})
	}
}

// A layer that is the zero value is a mistake at the call site, and it panics
// rather than returning an error — the judgement a bad target gets, for the
// same reason: a nil Layer is not a layer, nothing has one on purpose, and the
// call site has nothing to do with the error.
//
// This is the failure the layer selector exists to produce. While a string
// tenant carried the layer, a call site that dropped one wrote the global row
// and everything about the call looked right.
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
		{"Set", func() { _ = store.Set(ctx, Write{Key: "demo.limit", Value: 1}) }},
		{"Delete", func() { _ = store.Delete(ctx, nil, "demo.limit", "operator") }},
		{"List", func() { _, _ = store.List(ctx, nil) }},
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

// An empty identifier is a mistake at the call site too, and it panics at the
// constructor rather than at the operation. The tenant identifier's value
// domain has no empty string in it, so an empty one can only be a dropped
// value — and honouring it would act on every tenant at once.
func TestForTenantRejectsAnEmptyIdentifier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error(`ForTenant("") did not panic`)
		}
	}()
	_ = ForTenant("")
}

// TestForTenantRejectsAnIdentifierOutsideTheDomain covers the whole of the
// tenant identifier's value domain, not just the empty string. The directory's
// criteria are what this package can judge — an id is non-empty and free of
// blanks and control characters anywhere — and an id failing either names no
// tenant: a write would land on a row no tenant's read reaches, and a read would
// fall through to the shared layer and answer with a value the caller never
// asked for. The failures are silent, so the constructor refuses the id where it
// is given.
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
	// A long identifier is inside the domain: this package defines no length
	// for one, so it has none to refuse, and a case that started refusing a
	// long identifier would fail here.
	for _, id := range []string{"acme", "acme-1", "a.b", strings.Repeat("a", 200)} {
		t.Run("accepted/"+id, func(t *testing.T) {
			if got := ForTenant(id); got == nil {
				t.Errorf("ForTenant(%q) = nil, want a layer", id)
			}
		})
	}
}

// A value written under a type its declaration does not have is refused: the
// row would be unreadable under that key.
func TestWritingAValueOfTheWrongTypeIsAMismatch(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: "not a number"})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("writing a string to an int key returned %v, want ErrTypeMismatch", err)
	}
}

// A row whose text does not decode under its declaration is reported when a
// read lands on it, and every other key keeps working. The load itself does
// not fail: one bad value must not keep the process from starting.
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
	// Another tenant falls back to the global row and is unaffected.
	if got, err := Get[int](store, ForTenant("t2"), "demo.limit"); err != nil || got != 100 {
		t.Errorf("an unrelated read returned %v (%v), want the declared default", got, err)
	}
}

// A row that is present but does not decode is still what the layer held, and
// a write that replaces it reports that rather than reporting an empty layer.
//
// The two facts are different and a subscriber acts on them differently: an
// empty old value reads as "this key had no value here", while the row that
// was there says a value existed and was unreadable.
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

// The version is incremented by the statement that writes, and the value the
// event carries is the one that was just written.
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

// A sensitive item's change carries the fact and not the value, because an
// event reaches a transport and every subscriber on it.
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

// A global row's change is a system-level event: the envelope carries no
// tenant rather than a placeholder.
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

// A delete carries the value the row held and reports that it was a delete.
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

// A value over the size limit is refused before anything is written, since
// every written row is kept in memory.
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

// A value of exactly the limit is admitted. The boundary is stated as an upper
// bound on the text, so an implementation that refused it would narrow the
// contract with nothing else reporting the change.
func TestAValueOfExactlyTheLimitIsAdmitted(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	exact := strings.Repeat("x", maxValueBytes)
	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.label", Value: exact}); err != nil {
		t.Fatalf("writing a value of exactly the limit returned %v, want nil", err)
	}
}

// Delete clears a row whose declaration is gone.
//
// That is the one window in which a row for an undeclared key can still be
// reached: Get and Set both refuse such a key before the table is consulted,
// so refusing Delete too would leave the row with no way out through this
// interface at all.
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

// A delete of a sensitive item withholds the value it held, the way a write
// does: the delete path is the second place a payload is built, and a
// credential must not reach an event because the row was removed rather than
// written.
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

// List hands out the written rows of one layer, in key order, and nothing
// falls back.
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
	// The timestamp and the version are both part of Entry's contract, and a
	// column the scan does not map would leave either at its zero value with
	// nothing reporting the gap.
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

// Values round-trip: what a write encodes is what a read decodes, for every
// arm of the closed type set.
//
// Every arm is written and read back, because an arm the suite never reaches
// is an arm whose codec can be replaced by anything at all without a test
// noticing.
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

// A delivered change reloads the snapshot, which is how a replica learns about
// a write it did not make.
func TestADeliveredEventReloadsTheSnapshot(t *testing.T) {
	keys := demoTable(t)
	bus := newRecordingBus()
	store, handle := storeOf(t, keys, bus)
	store.subscribe()

	// A row written behind the store's back, as another replica would.
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

// A write of this replica reloads its own snapshot at once, so a read after a
// Set sees the new value without waiting for anything.
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

// A publish failure does not undo the write: the value is in the database, and
// reporting the write as failed would tell the caller the opposite of what
// happened.
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

// A load against a database without the table names the table and says what to
// do about it; it is not folded in with the other read failures.
func TestAMissingTableIsReportedAsSuch(t *testing.T) {
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	keys := demoTable(t)
	_, loadErr := loadSnapshot(context.Background(), handle, keys)
	if !errors.Is(loadErr, ErrTableMissing) {
		t.Fatalf("loading without the table returned %v, want ErrTableMissing", loadErr)
	}
	if !strings.Contains(loadErr.Error(), tableName) {
		t.Errorf("the error does not name the table %q: %v", tableName, loadErr)
	}
}

// A List failure names the layer it was listing once. describeLayer already
// returns a noun phrase with its article, so it goes straight into the noun
// slot: a second "the layer" in front of it would read "the layer the global
// layer".
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

// The statement placeholders follow the dialect, and the absent-table
// judgement reads each engine's own text.
//
// Neither branch can be exercised against a live engine here — the fixtures
// are SQLite throughout, and PostgreSQL asks for a service — so the two are
// pinned directly. Without this, flattening either would leave the suite green
// and reach the one backend this module is deployed against.
func TestTheDialectBranches(t *testing.T) {
	if got := bind(db.Postgres, 1); got != "$1" {
		t.Errorf("the first PostgreSQL placeholder is %q, want $1", got)
	}
	if got := bind(db.Postgres, 4); got != "$4" {
		t.Errorf("the fourth PostgreSQL placeholder is %q, want $4", got)
	}
	if got := bind(db.SQLite, 1); got != "?" {
		t.Errorf("the SQLite placeholder is %q, want ?", got)
	}

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

// eventOf builds the envelope a change would arrive on.
func eventOf(key string) event.Event {
	return event.Event{Type: ChangedEventType, Payload: Change{Key: key}}
}

// demoDialect names the engine the in-memory handle speaks.
func demoDialect() db.Dialect { return db.SQLite }

// countRows counts the rows in the settings table.
func countRows(t *testing.T, store *settingsStore) int {
	t.Helper()
	var n int
	if err := store.handle.Raw("SELECT count(*) FROM " + tableName).Row().Scan(&n); err != nil {
		t.Fatalf("counting the rows: %v", err)
	}
	return n
}

// collectingHandler keeps the lines a store writes.
type collectingHandler struct{ lines *[]string }

func (h collectingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h collectingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h collectingHandler) WithGroup(string) slog.Handler            { return h }

func (h collectingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.lines = append(*h.lines, r.Message)
	return nil
}

// TestReplacingTheSnapshotIsRecorded pins that the moment a snapshot takes
// effect is written down.
//
// Nothing else records it: reads serve whatever the current snapshot holds,
// and a row carries the time it was written rather than the time this process
// last read it. Without the line, a value that was written and is not the one
// being served leaves nothing to say how far behind the replica is — and that
// moment is what the propagation record points a reader at.
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

	// A reload that reads the same rows writes nothing. The refresh path runs
	// on a ticker, on a write and on an event, and a line per run would be a
	// line per request that writes a setting.
	lines = nil
	store.reload(context.Background())
	if len(lines) != 0 {
		t.Fatalf("a reload that changed nothing was recorded: %v", lines)
	}
}
