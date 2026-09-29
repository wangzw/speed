package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestConnectionScopedReadsTheLocatorTheWayTheDriverDoes pins which locators
// hold a handle to one connection. Reading one too narrowly leaves a private
// database under a pool, where tables appear and vanish between connections;
// reading one too widely takes the pool away from a database every connection
// shares.
func TestConnectionScopedReadsTheLocatorTheWayTheDriverDoes(t *testing.T) {
	cases := []struct {
		dsn    string
		scoped bool
	}{
		{"", true},
		{":memory:", true},
		{":memory:?_pragma=foreign_keys(1)", true},
		{"file::memory:", true},
		{"file:", true},
		{"file:scratch?mode=memory", true},
		{"file::memory:?cache=shared", false},
		{"file:scratch?mode=memory&cache=shared", false},
		{"/var/lib/app/app.db", false},
		{"/var/lib/app/app.db?_pragma=foreign_keys(1)", false},
		{"file:/var/lib/app/app.db?mode=rwc", false},
		// Outside a file: URI the parameters are the driver's, cut off
		// before SQLite sees the name, so cache=shared shares nothing.
		{":memory:?cache=shared", true},
		// SQLite takes a parameter named twice from its last occurrence.
		{"file:scratch?mode=rwc&mode=memory", true},
		{"file:scratch?mode=memory&mode=rwc", false},
		// It decodes escapes in the name, the parameters' names and their
		// values, and ends any of them at an escaped NUL.
		{"file:scratch?mod%65=memory", true},
		{"file:scratch?mode=memor%79", true},
		{"file:scratch?mode=memory%00rwc", true},
		{"file:scratch?mode%00x=memory", true},
		{"file:%3Amemory%3A", true},
		{"file::memory:%00/var/lib/app/app.db", true},
		// It ends the URI at a number sign.
		{"file:scratch?mode=memory#&mode=rwc", true},
		{"file:scratch?mode=memory&cache=shared#&cache=private", false},
		{"file::memory:#/var/lib/app/app.db", true},
		// The memdb VFS gives each connection a database of its own unless
		// the name begins with a slash, whether the URI or the driver's own
		// parameters name the VFS.
		{"file:scratch?vfs=memdb", true},
		{"file:/scratch?vfs=memdb", false},
		{"file:scratch#?vfs=memdb", true},
		{"scratch?vfs=memdb", true},
		{"/scratch?vfs=memdb", false},
		// A name shared under memdb is longer than one character, and begins
		// with a backslash as well as with a slash.
		{"file:/?vfs=memdb", true},
		{"/?vfs=memdb", true},
		{`\scratch?vfs=memdb`, false},
		// The driver hands its VFS to SQLite as a C string, which ends at a
		// NUL, escaped or not.
		{"scratch?vfs=memdb%00", true},
		{"scratch?vfs=memdb\x00", true},
		{"file:scratch#?vfs=memdb%00", true},
		{"/scratch?vfs=memdb%00", false},
		// It hands a file: URI to SQLite as a C string too.
		{"file:scratch?mode=memory\x00&mode=rwc", true},
		{"file:scratch?mode=rwc\x00&mode=memory", false},
		// A temporary database is its connection's even in shared-cache mode.
		{"file:?cache=shared", true},
	}
	for _, c := range cases {
		if got := connectionScoped(c.dsn); got != c.scoped {
			t.Errorf("connectionScoped(%q) = %v, want %v", c.dsn, got, c.scoped)
		}
	}
}

// TestSharesCacheReadsTheLocatorTheWayTheDriverDoes pins which locators put
// their connections in SQLite's shared-cache mode, which Dialector refuses.
// Reading one too narrowly opens a database whose lock waits nothing bounds.
func TestSharesCacheReadsTheLocatorTheWayTheDriverDoes(t *testing.T) {
	cases := []struct {
		dsn    string
		shared bool
	}{
		{"file::memory:?cache=shared", true},
		{"file:scratch?mode=memory&cache=shared", true},
		{"file:/var/lib/app/app.db?cache=shared", true},
		{"file:/var/lib/app/app.db?cache=private&cache=shared", true},
		{"file:/var/lib/app/app.db?cache=shared&cache=private", false},
		{"file:/var/lib/app/app.db?cach%65=shared", true},
		{"file:/var/lib/app/app.db?cache=shar%65d", true},
		{"file:/var/lib/app/app.db?cache=shared%00private", true},
		{"file:/var/lib/app/app.db?cache%00x=shared", true},
		{"file:/var/lib/app/app.db?cache=private", false},
		{"file:/var/lib/app/app.db?Cache=shared", false},
		{"file:/var/lib/app/app.db?cache=shared+", false},
		{"file:/var/lib/app/app.db", false},
		{":memory:", false},
		{"", false},
		// Outside a file: URI the parameters are the driver's.
		{":memory:?cache=shared", false},
		{"/var/lib/app/app.db?cache=shared", false},
		// SQLite ends the URI at a number sign, so what follows it neither
		// adds to a value nor overrides one.
		{"file:/var/lib/app/app.db?cache=shared#", true},
		{"file:/var/lib/app/app.db?cache=shared#&cache=private", true},
		{"file:/var/lib/app/app.db?cache=shared#x", true},
		{"file:/var/lib/app/app.db#?cache=shared", false},
		// The driver hands a file: URI to SQLite as a C string, which ends at
		// a NUL: what follows one neither adds to a value nor overrides one.
		{"file:/var/lib/app/app.db?cache=shared\x00x\x00y&cache=private", true},
		{"file:/var/lib/app/app.db?cache=private\x00&cache=shared", false},
		// A parameter with an empty name is skipped with its value.
		{"file:/var/lib/app/app.db?=private&cache=shared", true},
		// An authority of localhost is dropped; any other refuses the URI.
		{"file://localhost/var/lib/app/app.db?cache=shared", true},
		{"file:///var/lib/app/app.db?cache=shared", true},
		// A temporary database is never shared.
		{"file:?cache=shared", false},
		// The memdb VFS's databases share a cache like any other.
		{"file:/scratch?vfs=memdb&cache=shared", true},
	}
	for _, c := range cases {
		if got := sharesCache(c.dsn); got != c.shared {
			t.Errorf("sharesCache(%q) = %v, want %v", c.dsn, got, c.shared)
		}
	}
}

// locatorNames numbers the in-memory names the case below opens: a named
// in-memory database in shared-cache mode, and a memdb database whose name
// begins with a slash, are one database for the whole process.
var locatorNames atomic.Int64

// TestTheLocatorReadingAgreesWithTheDriver holds connectionScoped and
// sharesCache to what the driver and SQLite make of each locator, observed on
// two connections of one pool: whether the second sees a table the first
// created, and — where it does — whether it reads the first one's uncommitted
// row under read_uncommitted, which only a connection sharing the first one's
// cache can.
func TestTheLocatorReadingAgreesWithTheDriver(t *testing.T) {
	dir := t.TempDir()
	file := func(name string) string { return filepath.Join(dir, name) }
	memory := func() string { return fmt.Sprintf("zzlocator%d", locatorNames.Add(1)) }
	for _, dsn := range []string{
		"",
		":memory:",
		":memory:?cache=shared",
		"file::memory:",
		"file:",
		"file:?cache=shared",
		"file:" + memory() + "?mode=memory",
		"file:" + memory() + "?mode=memory&cache=shared",
		"file:" + memory() + "?mode=memory&cache=shared#&cache=private",
		"file:" + memory() + "?mode=memory#&mode=rwc",
		"file:" + memory() + "?mode=rwc&mode=memory",
		"file:" + memory() + "?mod%65=memory",
		"file:%3Amemory%3A",
		"file:" + memory() + "?vfs=memdb",
		"file:/" + memory() + "?vfs=memdb",
		"file:/" + memory() + "?vfs=memdb&cache=shared",
		"file:" + memory() + "#?vfs=memdb",
		memory() + "?vfs=memdb",
		"/" + memory() + "?vfs=memdb",
		"file:/?vfs=memdb",
		"/?vfs=memdb",
		`\` + memory() + "?vfs=memdb",
		memory() + "?vfs=memdb%00",
		memory() + "?vfs=memdb\x00",
		"file:" + memory() + "#?vfs=memdb%00",
		"/" + memory() + "?vfs=memdb%00",
		"file:" + memory() + "?mode=memory\x00&mode=rwc",
		file("plain.db"),
		file("driver-parameters.db") + "?cache=shared",
		"file:" + file("uri.db"),
		"file:" + file("shared.db") + "?cache=shared",
		"file:" + file("fragment.db") + "?cache=shared#",
		"file:" + file("fragment-private.db") + "?cache=shared#&cache=private",
		"file:" + file("last-private.db") + "?cache=shared&cache=private",
		"file:" + file("last-shared.db") + "?cache=private&cache=shared",
		"file:" + file("escaped.db") + "?cach%65=shar%65d",
		"file:" + file("empty-name.db") + "?=private&cache=shared",
		"file:" + file("raw-nul.db") + "?cache=shared\x00x\x00y&cache=private",
		"file:" + file("raw-nul-private.db") + "?cache=private\x00&cache=shared",
		"file://localhost" + file("authority.db") + "?cache=shared",
		"file:" + file("before-fragment.db") + "#?cache=shared",
	} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(dsn, dir, "<dir>"), "/", "|"), func(t *testing.T) {
			private, shared := observeLocator(t, dsn)
			if got := connectionScoped(dsn); got != private {
				t.Errorf("connectionScoped(%q) = %v, and the driver gives each connection a database of its "+
					"own: %v", dsn, got, private)
			}
			if got := sharesCache(dsn); got != shared {
				t.Errorf("sharesCache(%q) = %v, and the driver puts the connections in shared-cache mode: %v",
					dsn, got, shared)
			}
		})
	}
}

// observeLocator opens a locator through the driver this engine opens every
// connection with, and reports what two connections of one pool on it are to
// each other.
func observeLocator(t *testing.T, dsn string) (private, sharedCache bool) {
	t.Helper()
	ctx := context.Background()
	pool, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatalf("opening %q: %v", dsn, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	first, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("taking a connection on %q: %v", dsn, err)
	}
	defer func() { _ = first.Close() }()
	second, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("taking a second connection on %q: %v", dsn, err)
	}
	defer func() { _ = second.Close() }()

	if _, err = first.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS observed (id INTEGER)"); err != nil {
		t.Fatalf("creating a table on %q: %v", dsn, err)
	}
	var rows int64
	if err = second.QueryRowContext(ctx, "SELECT count(*) FROM observed").Scan(&rows); err != nil {
		if !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("reading the table on a second connection to %q: %v", dsn, err)
		}
		return true, false
	}
	// A connection outside the first one's cache waits for its lock, where
	// the memdb VFS keeps one, and gives up at once instead.
	for _, pragma := range []string{"PRAGMA read_uncommitted = 1", "PRAGMA busy_timeout = 0"} {
		if _, err = second.ExecContext(ctx, pragma); err != nil {
			t.Fatalf("%s on %q: %v", pragma, dsn, err)
		}
	}
	if _, err = first.ExecContext(ctx, "BEGIN"); err != nil {
		t.Fatalf("beginning a transaction on %q: %v", dsn, err)
	}
	defer func() { _, _ = first.ExecContext(ctx, "ROLLBACK") }()
	if _, err = first.ExecContext(ctx, "INSERT INTO observed (id) VALUES (1)"); err != nil {
		t.Fatalf("writing a row on %q: %v", dsn, err)
	}
	err = second.QueryRowContext(ctx, "SELECT count(*) FROM observed").Scan(&rows)
	switch {
	case err != nil && strings.Contains(err.Error(), "SQLITE_BUSY"):
		return false, false
	case err != nil:
		t.Fatalf("reading the table while the first connection writes it on %q: %v", dsn, err)
	}
	return false, rows == 1
}
