package settings

import (
	"context"
	"slices"
	"testing"

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
