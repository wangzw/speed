//go:build integration

package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

func init() {
	dialectsUnderTest = append(dialectsUnderTest, dialectUnderTest{
		name: "postgres", dialect: db.Postgres, open: openPostgres,
	})
	orderingDialects = append(orderingDialects, dialectUnderTest{
		name: "postgres", dialect: db.Postgres, open: openPostgresWithALinguisticCollation,
	})
}

func openPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle := quietly(dbtest.Postgres(t, pgtest.Acquire(t), nil).DB())
	applyMigration(t, handle, "postgres/0001_settings.sql")
	return handle
}

func openPostgresWithALinguisticCollation(t *testing.T) *gorm.DB {
	t.Helper()
	handle := openPostgres(t)
	for _, statement := range []string{
		"CREATE COLLATION linguistic (provider = icu, locale = 'en-u-ka-shifted')",
		"ALTER TABLE " + tableName + " ALTER COLUMN key TYPE TEXT COLLATE linguistic",
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("giving the key column a linguistic collation: %v", err)
		}
	}
	return handle
}

// TestTheStatementsRunOnPostgreSQL checks that the statements run against PostgreSQL as they do against SQLite.
func TestTheStatementsRunOnPostgreSQL(t *testing.T) {
	handle := openPostgres(t)
	bus := newRecordingBus()
	store := storeOn(t, handle, db.Postgres, demoTable(t), bus)
	ctx := context.Background()
	before := time.Now().UTC()

	for _, value := range []int{7, 8} {
		if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: value}); err != nil {
			t.Fatalf("writing %d: %v", value, err)
		}
	}
	if got, err := Get[int](store, ForTenant("t1"), "demo.limit"); err != nil || got != 8 {
		t.Errorf("read back %v (%v), want 8", got, err)
	}
	events := bus.events()
	if len(events) != 2 {
		t.Fatalf("want two events, got %d", len(events))
	}
	if first, second := events[0].Payload.(Change), events[1].Payload.(Change); first.Version != 1 ||
		second.Version != 2 || second.Old != "7" {
		t.Errorf("the changes are %+v and %+v, want versions 1 and 2 and the second's Old 7", first, second)
	}

	entries, err := store.List(ctx, ForTenant("t1"))
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("listed %+v, want one row", entries)
	}
	entry := entries[0]
	if entry.Tenant != "t1" || entry.Key != "demo.limit" || entry.Value != "8" || entry.Version != 2 {
		t.Errorf("the listed row is %+v, want t1's demo.limit at 8, version 2", entry)
	}
	if entry.UpdatedAt.Before(before.Add(-time.Minute)) || entry.UpdatedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("the listed row's timestamp %v is not the time of the write (%v)", entry.UpdatedAt, before)
	}

	if err := store.Delete(ctx, ForTenant("t1"), "demo.limit", "operator"); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if err := store.Delete(ctx, ForTenant("t1"), "demo.limit", "operator"); err != nil {
		t.Fatalf("deleting the absent row: %v", err)
	}
	events = bus.events()
	if len(events) != 3 {
		t.Fatalf("want one event for the removed row and none for the absent one, got %d in all", len(events))
	}
	if removed := events[2].Payload.(Change); !removed.Deleted || removed.Old != "8" {
		t.Errorf("the delete's change is %+v, want Deleted with Old 8", removed)
	}

	starts := time.Date(2026, 9, 21, 12, 30, 0, 0, time.UTC)
	for _, w := range []Write{
		{Layer: Global(), Key: "demo.window", Value: 90 * time.Second},
		{Layer: Global(), Key: "demo.count", Value: uint(7)},
		{Layer: Global(), Key: "demo.label", Value: "a label"},
		{Layer: Global(), Key: "demo.on", Value: true},
		{Layer: Global(), Key: "demo.ratio", Value: 0.125},
		{Layer: Global(), Key: "demo.starts-at", Value: starts},
	} {
		if err := store.Set(ctx, w); err != nil {
			t.Fatalf("writing %s: %v", w.Key, err)
		}
	}
	if got, _ := Get[time.Duration](store, Global(), "demo.window"); got != 90*time.Second {
		t.Errorf("the duration read back as %v", got)
	}
	if got, _ := Get[time.Time](store, Global(), "demo.starts-at"); !got.Equal(starts) {
		t.Errorf("the timestamp read back as %v, want %v", got, starts)
	}

	if err := handle.Exec("DROP TABLE " + tableName).Error; err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	if _, err := loadSnapshot(ctx, handle, demoTable(t)); !errors.Is(err, ErrTableMissing) {
		t.Errorf("loading without the table returned %v, want ErrTableMissing", err)
	}
}
