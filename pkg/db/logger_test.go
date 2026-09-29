package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

// The values the cases below bind, each one a mark the output is searched
// for.
const (
	slowValue   = "logged-value-slow"
	failedValue = "logged-value-failed"
	debugValue  = "logged-value-debugged"
)

// loggedEntry is a row of logged_entries.
type loggedEntry struct {
	ID    uint
	Value string
}

func (loggedEntry) TableName() string { return "logged_entries" }

// loggingOn is a handle whose statements log through a statementLogger writing
// to out with the threshold slow, on a database holding the table the cases
// write.
func loggingOn(t *testing.T, out *bytes.Buffer, slow time.Duration) *gorm.DB {
	t.Helper()
	statements := newStatementLogger(out)
	statements.slow = slow
	handle := dbsuite.OpenSQLite(t).Session(&gorm.Session{Logger: statements})
	if err := handle.Exec("CREATE TABLE logged_entries (id INTEGER PRIMARY KEY, value TEXT NOT NULL UNIQUE)").Error; err != nil {
		t.Fatalf("creating logged_entries: %v", err)
	}
	out.Reset()
	return handle
}

// TestTheLoggerWritesNoValueOnAnyPath holds the three ways a statement reaches
// the logger — slower than the threshold, failed, and every statement of a
// Debug session — to writing the statement's text and none of its values,
// and to naming the call site that issued it rather than the logger.
func TestTheLoggerWritesNoValueOnAnyPath(t *testing.T) {
	var out bytes.Buffer
	handle := loggingOn(t, &out, time.Nanosecond)
	debugging := loggingOn(t, &out, slowStatement).Debug()

	if err := handle.Create(&loggedEntry{ID: 1, Value: slowValue}).Error; err != nil {
		t.Fatalf("writing the slow row: %v", err)
	}
	if err := handle.Create(&loggedEntry{ID: 2, Value: slowValue}).Error; err == nil {
		t.Fatal("a second row under the unique value was written, want it refused")
	}
	var found []loggedEntry
	if err := debugging.Where("value = ?", debugValue).Find(&found).Error; err != nil {
		t.Fatalf("a debugged read failed: %v", err)
	}
	if err := handle.Where("value = ?", failedValue).First(&loggedEntry{}).Error; !errors.Is(err,
		gorm.ErrRecordNotFound) {
		t.Fatalf("looking up a value no row has gave %v, want no row", err)
	}

	written := out.String()
	for _, value := range []string{slowValue, failedValue, debugValue} {
		if strings.Contains(written, value) {
			t.Errorf("the output carries the bound value %q:\n%s", value, written)
		}
	}
	for _, want := range []string{
		"[warn] slow statement", "[error] statement failed", "[info]",
		"record not found", "logger_test.go:", "VALUES (?,?)", "value = ?",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the output does not carry %q:\n%s", want, written)
		}
	}
	if strings.Contains(written, "/logger.go:") {
		t.Errorf("the output names the logger as the call site:\n%s", written)
	}
}

// quotingError is an error whose text quotes a value, as PostgreSQL's refusal
// of a malformed input does, and which carries an engine's code.
type quotingError struct{ value string }

func (e quotingError) Error() string    { return fmt.Sprintf("invalid input syntax: %q", e.value) }
func (e quotingError) SQLState() string { return "22P02" }

// TestAFailedStatementIsWrittenWithTheClassesOfItsError covers the error half:
// the text of an error can quote what the statement bound or read, so the
// logger writes the classes the error carries — the engine's code, GORM's
// sentinels, a context's end — and never the text.
func TestAFailedStatementIsWrittenWithTheClassesOfItsError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{quotingError{failedValue}, "statement failed: SQLSTATE 22P02"},
		{fmt.Errorf("writing %s: %w", failedValue, gorm.ErrDuplicatedKey), "statement failed: duplicated key not allowed"},
		{fmt.Errorf("%s: %w", failedValue, context.DeadlineExceeded), "statement failed: context deadline exceeded"},
		{errors.New(failedValue), "statement failed"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		statements := newStatementLogger(&out)
		statements.Trace(context.Background(), time.Now(), func() (string, int64) { return "SELECT ?", -1 }, c.err)
		written := out.String()
		if strings.Contains(written, failedValue) {
			t.Errorf("the failure %v is written with its text: %s", c.err, written)
		}
		if !strings.Contains(written, c.want+" [") || !strings.Contains(written, "[rows:-] SELECT ?") {
			t.Errorf("the failure %v is written as %q, want %q and the statement", c.err, written, c.want)
		}
	}
}

// TestTheLoggerWritesByItsLevel holds the logger to GORM's levels: at the
// default one it writes failed and slow statements and GORM's warnings and
// errors, not its informational messages; at Error only failures; Silent
// nothing.
func TestTheLoggerWritesByItsLevel(t *testing.T) {
	ctx := context.Background()
	failed := errors.New("refused")
	emit := func(level logger.LogLevel) string {
		var out bytes.Buffer
		statements := newStatementLogger(&out)
		statements.slow = time.Nanosecond
		l := statements.LogMode(level)
		l.Info(ctx, "an informational message")
		l.Warn(ctx, "a warning")
		l.Error(ctx, "an error")
		l.Trace(ctx, time.Now().Add(-time.Second), func() (string, int64) { return "SELECT slow", 0 }, nil)
		l.Trace(ctx, time.Now(), func() (string, int64) { return "SELECT failed", 0 }, failed)
		return out.String()
	}
	cases := []struct {
		level          logger.LogLevel
		written, never []string
	}{
		{logger.Warn, []string{"a warning", "an error", "SELECT slow", "SELECT failed"}, []string{"an informational message"}},
		{logger.Error, []string{"an error", "SELECT failed"}, []string{"a warning", "SELECT slow"}},
		{logger.Silent, nil, []string{"an error", "SELECT failed", "SELECT slow"}},
		{logger.Info, []string{"an informational message", "SELECT slow", "SELECT failed"}, nil},
	}
	for _, c := range cases {
		written := emit(c.level)
		for _, want := range c.written {
			if !strings.Contains(written, want) {
				t.Errorf("at level %d the logger did not write %q:\n%s", c.level, want, written)
			}
		}
		for _, unwanted := range c.never {
			if strings.Contains(written, unwanted) {
				t.Errorf("at level %d the logger wrote %q:\n%s", c.level, unwanted, written)
			}
		}
	}
}
