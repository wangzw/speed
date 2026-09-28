package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

// deliver runs a real assembly around this subpackage's module, with the
// widgets migration declared and the given items configured beside the
// locator, and returns the capability once the migrations are applied. The
// assembly is shut down when the case ends.
func deliver(t *testing.T, dsn string, items ...string) db.Database {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q`, dsn)
	for _, item := range items {
		body += "," + item
	}
	body += "}}}"
	probe := newProbe()
	reg := assembly(t, body,
		sqlite.Module(),
		core.Module{Name: "catalog", Resources: []any{db.Migrations{FS: fstest.MapFS{
			"sqlite/0001_create_widgets.sql": &fstest.MapFile{Data: []byte(createWidgets)},
		}}}},
		probe.module(),
	)
	previous := os.Args
	os.Args = []string{"sqlite.test"}
	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx) }()

	var instance db.Database
	select {
	case instance = <-probe.delivered:
	case err := <-done:
		os.Args = previous
		cancel()
		t.Fatalf("the assembly over %q did not deliver the database capability: %v", dsn, err)
	}
	select {
	case err := <-done:
		os.Args = previous
		cancel()
		t.Fatalf("the assembly over %q shut down before its Init stage: %v", dsn, err)
	case <-probe.migrated:
	}
	os.Args = previous
	t.Cleanup(func() {
		close(probe.gate)
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the assembly shut down with an error: %v", err)
		}
	})
	return instance
}

// concurrentInserts writes rows into widgets from several goroutines at once,
// which is how requests reach a pool, and returns the failures.
func concurrentInserts(handle *gorm.DB, rows int) []error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures []error
	)
	for id := 1; id <= rows; id++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := handle.Exec("INSERT INTO widgets (id) VALUES (?)", id).Error; err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failures
}

// TestAnInMemoryLocatorIsOneDatabaseForTheWholeHandle pins what a locator
// naming an in-memory database gives a host: one database. SQLite gives each
// connection that opens such a locator a database of its own, so over a pool
// the migrations land on the connection the run was pinned to and a request on
// any other connection finds no table — the tables seem to come and go, and
// rows written on one connection are missing on the next. The module holds
// such a handle to one connection, which is the only shape in which the
// locator means what it says.
func TestAnInMemoryLocatorIsOneDatabaseForTheWholeHandle(t *testing.T) {
	const rows = 20
	instance := deliver(t, ":memory:")
	if failures := concurrentInserts(instance.DB(), rows); len(failures) > 0 {
		t.Fatalf("%d of %d concurrent inserts into the migrated table failed on the delivered handle, the "+
			"first with: %v", len(failures), rows, failures[0])
	}
	var count int64
	if err := instance.DB().Table("widgets").Count(&count).Error; err != nil {
		t.Fatalf("counting the rows: %v", err)
	}
	if count != rows {
		t.Errorf("the delivered handle sees %d rows, want the %d written", count, rows)
	}

	for _, locator := range []string{":memory:", "file::memory:", "file:scratch?mode=memory"} {
		opened, err := instance.Open(t.Context(), locator)
		if err != nil {
			t.Fatalf("Open(%q): %v", locator, err)
		}
		t.Cleanup(func() { _ = db.Close(opened) })
		if err := opened.Exec(createWidgets).Error; err != nil {
			t.Fatalf("creating the table through Open(%q): %v", locator, err)
		}
		if failures := concurrentInserts(opened, rows); len(failures) > 0 {
			t.Errorf("%d of %d concurrent inserts failed on Open(%q), the first with: %v",
				len(failures), rows, locator, failures[0])
		}
	}
}

// TestAnInMemoryDatabaseOutlivesThePoolParameters pins the other half of one
// connection: it is never replaced. A pool that retires a connection for its
// age, or keeps none idle between statements, closes the in-memory database
// with it and opens an empty one in its place, so the migrated table and the
// rows in it are gone with no error until the next statement cannot find them.
func TestAnInMemoryDatabaseOutlivesThePoolParameters(t *testing.T) {
	const lifetime = 20 * time.Millisecond
	instance := deliver(t, ":memory:",
		fmt.Sprintf(`"conn-max-lifetime":%q`, lifetime.String()), `"max-idle-conns":0`)
	if err := instance.DB().Exec("INSERT INTO widgets (id) VALUES (1)").Error; err != nil {
		t.Fatalf("writing a row into the migrated table: %v", err)
	}
	time.Sleep(5 * lifetime)
	var count int64
	if err := instance.DB().Table("widgets").Count(&count).Error; err != nil {
		t.Fatalf("the migrated table is gone after the configured lifetime passed: %v", err)
	}
	if count != 1 {
		t.Errorf("the handle sees %d rows after the configured lifetime passed, want the 1 written", count)
	}
}

// TestAShareableLocatorKeepsTheConfiguredPool keeps the single connection to
// the locators that need it. A file, and an in-memory database opened with a
// shared cache, are one database for every connection, so the pool keeps the
// shape the configuration gave it.
func TestAShareableLocatorKeepsTheConfiguredPool(t *testing.T) {
	instance := deliver(t, filepath.Join(t.TempDir(), "shared.db"))
	pool, err := instance.DB().DB()
	if err != nil {
		t.Fatalf("reaching the delivered pool: %v", err)
	}
	if got := pool.Stats().MaxOpenConnections; got <= 1 {
		t.Errorf("a file database holds at most %d connection, want the configured pool", got)
	}
	shared, err := instance.Open(t.Context(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("Open on a shared in-memory database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(shared) })
	sharedPool, err := shared.DB()
	if err != nil {
		t.Fatalf("reaching the shared pool: %v", err)
	}
	if got := sharedPool.Stats().MaxOpenConnections; got <= 1 {
		t.Errorf("a shared in-memory database holds at most %d connection, want the configured pool", got)
	}
}
