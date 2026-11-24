package db

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// These cases live in package db because the opener is unexported.

// locatorSecret stands where a locator carries credentials. Nothing this
// package reports may contain it: these errors reach the startup diagnostics,
// and the diagnostics are written to a stream the host cannot redirect.
const locatorSecret = "hunter2-must-not-be-echoed"

func sqliteSpec() Spec {
	return Spec{
		ModuleName:          sqliteNamespace,
		ConfigNamespace:     sqliteNamespace,
		Dialect:             SQLite,
		Dialector:           sqlite.Open,
		ConstraintViolation: sqliteViolation,
	}
}

func sqliteSection(dsn string) lockingConfig {
	return lockingConfig{Config: Config{
		DSN:             dsn,
		MaxOpenConns:    defaultMaxOpenConns,
		MaxIdleConns:    defaultMaxIdleConns,
		ConnMaxLifetime: defaultConnMaxLifetime,
	}}
}

// discardingLogger is the logger a case hands to openHandle or newDatabase when
// it does not read what the handle writes: one over slog.DiscardHandler, which
// accepts every record and keeps none.
func discardingLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func unopenableDSN(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), locatorSecret, "db.sqlite")
}

func poolOf(t *testing.T, handle *gorm.DB) *sql.DB {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the connection pool: %v", err)
	}
	return pool
}

// releaseAtEnd gives a pool back when the case ends, so that a case which
// asserted on what the pool did does not leave it open for the rest of the run.
func releaseAtEnd(t *testing.T, pool *sql.DB) {
	t.Helper()
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("releasing the connection pool: %v", err)
		}
	})
}

// TestConnectFailureIsErrConnectFailed pins the sentinel a locator the driver cannot open reports.
func TestConnectFailureIsErrConnectFailed(t *testing.T) {
	_, err := openHandle(t.Context(), sqliteSpec(), sqliteSection(unopenableDSN(t)), discardingLogger())
	if !errors.Is(err, ErrConnectFailed) {
		t.Fatalf("opening a locator the driver refuses gave %v, want ErrConnectFailed", err)
	}
}

// TestConnectFailureDoesNotEchoTheLocator keeps the credentials a locator carries out of the error
// text, which is written to the startup diagnostics and which the host cannot redirect.
func TestConnectFailureDoesNotEchoTheLocator(t *testing.T) {
	_, err := openHandle(t.Context(), sqliteSpec(), sqliteSection(unopenableDSN(t)), discardingLogger())
	if err == nil {
		t.Fatal("opening a locator whose directory does not exist succeeded")
	}
	text := err.Error()
	if strings.Contains(text, locatorSecret) {
		t.Errorf("the error echoes the locator, which carries the credentials it is refused "+
			"for: %v", err)
	}
	if !strings.Contains(text, string(SQLite)) {
		t.Errorf("the error does not name the dialect: %v", err)
	}
	if !strings.Contains(text, "opening") {
		t.Errorf("the error does not name the step that failed: %v", err)
	}
}

// TestConnectFailureNamesTheConnectivityStep pins the second step a connection establishment has,
// and that it is bound to the context the stage received.
func TestConnectFailureNamesTheConnectivityStep(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := openHandle(ctx, sqliteSpec(), sqliteSection(filepath.Join(t.TempDir(), "cancelled.db")), discardingLogger())
	if !errors.Is(err, ErrConnectFailed) {
		t.Fatalf("connecting under a cancelled context gave %v, want ErrConnectFailed", err)
	}
	if text := err.Error(); !strings.Contains(text, "connectivity check") {
		t.Errorf("the error does not name the connectivity check as the step that failed: %v", err)
	}
}

// TestTheLocatorReachesTheDriverTrimmed is the half the stance cannot hold on its own.
func TestTheLocatorReachesTheDriverTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trimmed.db")
	var seen string
	spec := sqliteSpec()
	spec.Dialector = func(dsn string) gorm.Dialector {
		seen = dsn
		return sqlite.Open(dsn)
	}
	handle, err := openHandle(t.Context(), spec, sqliteSection(" "+path+" "), discardingLogger())
	if err != nil {
		t.Fatalf("opening against a padded locator: %v", err)
	}
	sqlDB, err := handle.DB()
	if err != nil {
		t.Fatalf("the session came back without a pool: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if seen != path {
		t.Fatalf("the driver was handed %q, want %q", seen, path)
	}
}

// TestPoolParametersReachTheConnection pins that the largest number of connections the pool opens
// is the configured one.
func TestPoolParametersReachTheConnection(t *testing.T) {
	cfg := sqliteSection(filepath.Join(t.TempDir(), "pool.db"))
	cfg.MaxOpenConns = 7

	handle, err := openHandle(t.Context(), sqliteSpec(), cfg, discardingLogger())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	pool := poolOf(t, handle)
	releaseAtEnd(t, pool)
	if got := pool.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("the pool opens at most %d connections, want the configured 7", got)
	}
}

// TestIdleConnectionsReachTheConnection pins the other end of the pool: how many connections are
// kept once they are no longer in use.
func TestIdleConnectionsReachTheConnection(t *testing.T) {
	ctx := t.Context()
	cfg := sqliteSection(filepath.Join(t.TempDir(), "idle.db"))
	cfg.MaxOpenConns = 8
	cfg.MaxIdleConns = 3

	handle, err := openHandle(ctx, sqliteSpec(), cfg, discardingLogger())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	pool := poolOf(t, handle)
	releaseAtEnd(t, pool)

	held := make([]*sql.Conn, 0, 8)
	for range 8 {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatalf("taking connection %d of 8: %v", len(held)+1, err)
		}
		held = append(held, conn)
	}
	for _, conn := range held {
		if err := conn.Close(); err != nil {
			t.Fatalf("giving a connection back: %v", err)
		}
	}
	if got := pool.Stats().Idle; got != 3 {
		t.Errorf("the pool kept %d connections idle, want the configured 3", got)
	}
}

// TestConnMaxLifetimeReachesTheConnection pins the third parameter: how long a connection is reused
// before it is replaced.
func TestConnMaxLifetimeReachesTheConnection(t *testing.T) {
	ctx := t.Context()
	cfg := sqliteSection(filepath.Join(t.TempDir(), "lifetime.db"))
	cfg.ConnMaxLifetime = time.Nanosecond

	handle, err := openHandle(ctx, sqliteSpec(), cfg, discardingLogger())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	pool := poolOf(t, handle)
	releaseAtEnd(t, pool)

	for round := 1; round <= 3; round++ {
		if err := pool.PingContext(ctx); err != nil {
			t.Fatalf("pinging after round %d: %v", round, err)
		}
	}
	if got := pool.Stats().MaxLifetimeClosed; got == 0 {
		t.Error("no connection was replaced once its lifetime had passed, so conn-max-lifetime " +
			"never reached the pool")
	}
}

const openedValue = "logged-value-opened"

// TestAHandleWritesItsStatementsThroughTheLoggerItIsOpenedWith holds the one place that builds the
// statement logger to building it over the logger it is handed.
func TestAHandleWritesItsStatementsThroughTheLoggerItIsOpenedWith(t *testing.T) {
	rec := &recordingHandler{}
	handle, err := openHandle(t.Context(), sqliteSpec(), sqliteSection(filepath.Join(t.TempDir(), "logged.db")),
		slog.New(rec))
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	t.Cleanup(func() {
		if err := Close(handle); err != nil {
			t.Errorf("releasing the handle: %v", err)
		}
	})

	if err := handle.Exec("CREATE TABLE logged_codes (code TEXT NOT NULL UNIQUE)").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := handle.Exec("INSERT INTO logged_codes (code) VALUES (?)", openedValue).Error; err != nil {
		t.Fatalf("writing the row: %v", err)
	}
	if records := withoutSlowStatements(rec.written()); len(records) != 0 {
		t.Fatalf("statements that ran as any other were written at the level a handle is opened at:\n%s",
			everythingOf(records))
	}

	if err := handle.Exec("INSERT INTO logged_codes (code) VALUES (?)", openedValue).Error; !errors.Is(err,
		gorm.ErrDuplicatedKey) {
		t.Fatalf("a second row under the unique value gave %v, want gorm.ErrDuplicatedKey in its chain", err)
	}
	records := rec.written()
	assertNoneCarries(t, records, openedValue)
	written := withoutSlowStatements(records)
	if len(written) != 1 {
		t.Fatalf("the refused statement was written as %d records, want one:\n%s", len(written), everythingOf(records))
	}
	attrs := attributesOf(t, written[0])
	if written[0].Message != "statement failed" || written[0].Level != slog.LevelError {
		t.Errorf("the refused statement is written as %q at %s, want \"statement failed\" at ERROR",
			written[0].Message, written[0].Level)
	}
	if classes, _ := textOf(attrs, "classes"); classes != "duplicated key not allowed, result code 2067" {
		t.Errorf("the refused statement is written with the classes %q, want the sentinel and the engine's code",
			classes)
	}
	if statement, _ := textOf(attrs, "statement"); statement != "INSERT INTO logged_codes (code) VALUES (?)" {
		t.Errorf("the refused statement is written as %q, want its text with the placeholder", statement)
	}
}

// TestAHandleOpenedWithNoLoggerIsRefusedBeforeAnythingIsOpened holds openHandle to refusing a nil
// logger with a message.
func TestAHandleOpenedWithNoLoggerIsRefusedBeforeAnythingIsOpened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unlogged.db")

	var reported any
	func() {
		defer func() { reported = recover() }()
		_, _ = openHandle(t.Context(), sqliteSpec(), sqliteSection(path), nil)
	}()

	if reported == nil {
		t.Fatal("openHandle accepted a nil logger")
	}
	if text, ok := reported.(string); !ok || text == "" {
		t.Errorf("openHandle panicked with %v (%T), want a message", reported, reported)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the database file exists after the refusal, or cannot be told not to (%v): the connection was "+
			"made before the logger was judged", err)
	}
}
