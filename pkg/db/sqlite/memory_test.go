package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestAnInMemoryLocatorIsOneDatabaseForTheWholeHandle pins what a locator naming an in- memory
// database gives a host: one database.
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

	for _, locator := range []string{
		":memory:", "file::memory:", "file:scratch?mode=memory", "file:%3Amemory%3A",
		"file:scratch?vfs=memdb", "scratch?vfs=memdb", "file:scratch?mode=memory#&mode=rwc",
	} {
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

// TestAnInMemoryDatabaseOutlivesThePoolParameters pins the other half of one connection: it is
// never replaced.
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

// TestAShareableLocatorKeepsTheConfiguredPool keeps the single connection to the locators that need
// it.
func TestAShareableLocatorKeepsTheConfiguredPool(t *testing.T) {
	dir := t.TempDir()
	instance := deliver(t, filepath.Join(dir, "shared.db"))
	pool, err := instance.DB().DB()
	if err != nil {
		t.Fatalf("reaching the delivered pool: %v", err)
	}
	if got := pool.Stats().MaxOpenConnections; got <= 1 {
		t.Errorf("a file database holds at most %d connection, want the configured pool", got)
	}
	opened, err := instance.Open(t.Context(), "file:"+filepath.Join(dir, "opened.db")+"?mode=rwc")
	if err != nil {
		t.Fatalf("Open on a file named as a file: URI: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(opened) })
	openedPool, err := opened.DB()
	if err != nil {
		t.Fatalf("reaching the opened pool: %v", err)
	}
	if got := openedPool.Stats().MaxOpenConnections; got <= 1 {
		t.Errorf("a file named as a file: URI holds at most %d connection, want the configured pool", got)
	}
}

func sharedCacheLocators(dir string) map[string]string {
	file := "file:" + filepath.Join(dir, "cached.db")
	return map[string]string{
		"the unnamed in-memory database":         "file::memory:?cache=shared",
		"a named in-memory database":             "file:scratch?mode=memory&cache=shared",
		"a file":                                 file + "?cache=shared",
		"a file whose last cache parameter wins": file + "?cache=private&cache=shared",
		"a file whose URI a number sign ends":    file + "?cache=shared#&cache=private",
		"a named in-memory database of memdb's":  "file:/scratch?vfs=memdb&cache=shared",
	}
}

// TestALocatorInSharedCacheModeIsRefused pins what this engine does with a locator that puts its
// connections in SQLite's shared-cache mode: it opens nothing.
func TestALocatorInSharedCacheModeIsRefused(t *testing.T) {
	dir := t.TempDir()
	instance := deliver(t, filepath.Join(dir, "delivered.db"))
	for name, locator := range sharedCacheLocators(dir) {
		t.Run("Open on "+name, func(t *testing.T) {
			handle, err := instance.Open(t.Context(), locator)
			if err == nil {
				_ = db.Close(handle)
				t.Fatalf("Open(%q) returned a handle", locator)
			}
			if !errors.Is(err, db.ErrConnectFailed) || !strings.Contains(err.Error(), "shared-cache") {
				t.Errorf("Open(%q) gave %v, want ErrConnectFailed naming the shared-cache mode", locator, err)
			}
		})
		t.Run("delivered on "+name, func(t *testing.T) {
			err := assemblingOver(t, locator)
			if !errors.Is(err, db.ErrConnectFailed) || !strings.Contains(err.Error(), "shared-cache") {
				t.Errorf("an assembly over %q gave %v, want ErrConnectFailed naming the shared-cache mode",
					locator, err)
			}
		})
	}
}

func assemblingOver(t *testing.T, dsn string) error {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)
	reader, err := load(t, body, sqlite.Module())
	if err != nil {
		t.Fatalf("loading the configuration over %q: %v", dsn, err)
	}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Supply(reader, config.ModuleName)
	return reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{sqlite.ModuleName}})
}
