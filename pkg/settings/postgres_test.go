package settings

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// openPostgres hands back a handle on a PostgreSQL database of this test's
// own, opened through the dialector the PostgreSQL implementation of the
// database module uses, with this module's PostgreSQL migration applied. It
// skips on a machine without docker, and fails instead where CI is set.
func openPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(postgres.Dialector(pgtest.Acquire(t)), quiet())
	if err != nil {
		t.Fatalf("opening the PostgreSQL fixture: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the fixture's connection pool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing the fixture: %v", err)
		}
	})
	applyMigration(t, handle, "postgres/0001_settings.sql")
	return handle
}

// storeOn builds a store over a handle of the given dialect, with the snapshot
// already loaded.
func storeOn(t *testing.T, handle *gorm.DB, dialect db.Dialect, keys *keyTable, bus event.Bus) *settingsStore {
	t.Helper()
	s := newSettingsStore(keys, stubDatabase{handle: handle, dialect: dialect}, bus, moduleSettings{})
	s.logger = discardLogger()
	if err := s.load(context.Background()); err != nil {
		t.Fatalf("loading the snapshot: %v", err)
	}
	return s
}

// The statements run against PostgreSQL as they do against SQLite: the
// placeholders GORM rewrites for the dialect, the upsert that reads its version
// back, the delete that reads its removed row back, the timestamp column List
// scans, and the absent-table judgement on the text the PostgreSQL driver
// actually produces.
//
// The statements are written once for both dialects, so each of these is a
// claim that the two engines agree on the same text. A suite that ran on
// SQLite alone would stay green while the one backend this module is deployed
// against failed.
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

// ordering declares keys whose byte order differs from the order a collation
// that sets punctuation aside gives them.
type ordering struct {
	AZ     string `config:"a-z"`
	AB     string `config:"ab"`
	ADashB string `config:"a-b"`
	AA     string `config:"aa"`
}

// List orders the rows by the key's bytes, on either dialect and under any
// collation the database was created with.
//
// SQLite compares text byte by byte. PostgreSQL compares it under the
// database's collation, and the one a PostgreSQL server built against the GNU
// C library is created with by default, en_US.UTF-8, sets punctuation aside
// at its first level — so a-b sorts next to ab rather than before aa, and the
// same rows came back in a different order depending on the dialect a command
// ran on. An ICU collation with alternate=shifted stands for that default
// here: the fixture's server is built against a C library whose collations
// compare bytes.
func TestListOrdersByTheKeysBytesOnEveryDialect(t *testing.T) {
	want := []string{"order.a-b", "order.a-z", "order.aa", "order.ab"}
	if !slices.IsSorted(want) {
		t.Fatalf("the expected order %v is not the keys' byte order", want)
	}
	dialects := []struct {
		name    string
		dialect db.Dialect
		open    func(t *testing.T) *gorm.DB
	}{
		{"sqlite", db.SQLite, open},
		{"postgres", db.Postgres, func(t *testing.T) *gorm.DB {
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
		}},
	}
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			keys := tableOf(t, declaring("order", Schema{Namespace: "order", Mounts: []Mount{{Value: &ordering{}}}}))
			store := storeOn(t, d.open(t), d.dialect, keys, newRecordingBus())
			ctx := context.Background()
			for _, key := range []string{"order.ab", "order.aa", "order.a-z", "order.a-b"} {
				if err := store.Set(ctx, Write{Layer: Global(), Key: key, Value: "x"}); err != nil {
					t.Fatalf("writing %s: %v", key, err)
				}
			}
			entries, err := store.List(ctx, Global())
			if err != nil {
				t.Fatalf("listing: %v", err)
			}
			got := make([]string, 0, len(entries))
			for _, e := range entries {
				got = append(got, e.Key)
			}
			if !slices.Equal(got, want) {
				t.Errorf("List returned %v, want the byte order %v", got, want)
			}
		})
	}
}
