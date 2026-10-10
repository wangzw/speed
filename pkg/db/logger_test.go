package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
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

	scanFailedValue = "logged-value-scan-failed"
	scanSlowValue   = "logged-value-scan-slow"
	scanDebugValue  = "logged-value-scan-debugged"
	scanChainValue  = "logged-value-scan-chained"
	scanIdleValue   = "logged-value-scan-idle"
	rowDebugValue   = "logged-value-row-debugged"
	filterValue     = "logged-value-filter-probe"
)

// withheldText is what a line carries in place of the text of a statement
// GORM's Scan finisher has filled its values into. It is written out here, not
// read from the logger, so that rewording it is a change these cases see.
const withheldText = "statement withheld: GORM filled its values in before the logger saw it"

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
// and to naming the call site that issued it rather than the logger. The
// statements it issues are the ones GORM renders for the logger; the ones the
// Scan finisher runs are held by
// TestAStatementRunThroughScanIsWrittenWithoutItsText.
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

// TestAStatementRunThroughScanIsWrittenWithoutItsText holds the logger to the
// one path on which GORM never asks it for the values a statement binds: the
// Scan finisher swaps the handle's logger for a recorder, whose ParamsFilter
// hands every value back, runs the statement on the recorder, and gives the
// handle's logger the finished text, values in, through Trace alone. The
// logger's own ParamsFilter is not asked there, so what the logger can do is
// withhold the text. Whichever way the statement reaches the logger — failed,
// slower than the threshold, in a Debug session — and however it is built, its
// line carries no value and has the withheld text where the statement would
// be, and it still carries what every line carries: the classes of the error,
// the time, the rows and the call site.
func TestAStatementRunThroughScanIsWrittenWithoutItsText(t *testing.T) {
	cases := []struct {
		name  string
		value string
		slow  time.Duration
		debug bool
		// issue runs the statement on a handle that holds a row under value
		// and returns the error it reports, which fails says whether to expect.
		issue func(handle *gorm.DB, value string) error
		fails bool
		// head is how the line begins after the call site, and rows what it
		// counts, as a pattern.
		head, rows string
	}{
		{
			name: "failed", value: scanFailedValue, slow: slowStatement, fails: true,
			issue: func(handle *gorm.DB, value string) error {
				var id uint
				return handle.Raw("INSERT INTO logged_entries (id, value) VALUES (2, ?) RETURNING id", value).Scan(&id).Error
			},
			head: "[error] statement failed: result code 2067", rows: `(-|\d+)`,
		},
		{
			name: "slow", value: scanSlowValue, slow: time.Nanosecond,
			issue: func(handle *gorm.DB, value string) error {
				var id uint
				return handle.Raw("SELECT id FROM logged_entries WHERE value = ?", value).Scan(&id).Error
			},
			head: "[warn] slow statement >= 1ns", rows: "1",
		},
		{
			name: "debugged", value: scanDebugValue, slow: slowStatement, debug: true,
			issue: func(handle *gorm.DB, value string) error {
				var id uint
				return handle.Raw("SELECT id FROM logged_entries WHERE value = ?", value).Scan(&id).Error
			},
			head: "[info]", rows: "1",
		},
		{
			name: "chained", value: scanChainValue, slow: slowStatement, debug: true,
			issue: func(handle *gorm.DB, value string) error {
				var ids []uint
				return handle.Model(&loggedEntry{}).Select("id").Where("value = ?", value).Scan(&ids).Error
			},
			head: "[info]", rows: "1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			handle := loggingOn(t, &out, c.slow)
			if err := handle.Create(&loggedEntry{ID: 1, Value: c.value}).Error; err != nil {
				t.Fatalf("writing the row the statement meets: %v", err)
			}
			if c.debug {
				handle = handle.Debug()
			}
			out.Reset()
			if err := c.issue(handle, c.value); (err != nil) != c.fails {
				t.Fatalf("the statement reported %v, want a failure: %t", err, c.fails)
			}

			written := out.String()
			if strings.Contains(written, c.value) {
				t.Errorf("the output carries the bound value %q:\n%s", c.value, written)
			}
			lines := strings.Split(strings.TrimSuffix(written, "\n"), "\n")
			if len(lines) != 1 {
				t.Fatalf("the statement was written as %d lines, want one:\n%s", len(lines), written)
			}
			if !strings.Contains(lines[0], "/logger_test.go:") || strings.Contains(lines[0], "/logger.go:") {
				t.Errorf("the line does not name this file as the call site:\n%s", lines[0])
			}
			ending := regexp.MustCompile(` ` + regexp.QuoteMeta(c.head) + ` \[\d+\.\d{3}ms\] \[rows:` + c.rows + `\] ` +
				regexp.QuoteMeta(withheldText) + `$`)
			if !ending.MatchString(lines[0]) {
				t.Errorf("the line does not end with %q, its time, its rows and the withheld text:\n%s",
					c.head, lines[0])
			}
		})
	}
}

// TestAStatementRunThroughRowIsStillWrittenWithItsText is the control of the
// case above: Row, unlike the Scan finisher, leaves the handle's logger in
// place, so GORM asks the logger's ParamsFilter for the values and in a Debug
// session its line still names the statement with its placeholder and carries
// no value. The text is withheld only where it has to be.
func TestAStatementRunThroughRowIsStillWrittenWithItsText(t *testing.T) {
	var out bytes.Buffer
	handle := loggingOn(t, &out, slowStatement).Debug()

	var id uint
	if err := handle.Raw("SELECT id FROM logged_entries WHERE value = ?", rowDebugValue).Row().Scan(&id); !errors.Is(err,
		sql.ErrNoRows) {
		t.Fatalf("looking up a value no row has gave %v, want no row", err)
	}

	written := out.String()
	if strings.Contains(written, rowDebugValue) {
		t.Errorf("the output carries the bound value %q:\n%s", rowDebugValue, written)
	}
	for _, want := range []string{"[info]", "/logger_test.go:", "SELECT id FROM logged_entries WHERE value = ?"} {
		if !strings.Contains(written, want) {
			t.Errorf("the output does not carry %q:\n%s", want, written)
		}
	}
	if strings.Contains(written, withheldText) {
		t.Errorf("the output withholds a statement GORM rendered with its placeholders:\n%s", written)
	}
}

// TestAScanThatRanNoStatementIsNotWrittenAsWithheld holds the withheld text to
// the statements that exist. A Scan on a handle that already carries an error
// runs nothing, and on a chain that has not built its statement yet — a Model
// with a Where, no Raw — there is none to hand over: GORM still gives the
// logger the handle's error through Trace, with the empty text of a recorder
// that saw no statement. That line has nothing to withhold, and does not say it
// withheld one.
func TestAScanThatRanNoStatementIsNotWrittenAsWithheld(t *testing.T) {
	var out bytes.Buffer
	handle := loggingOn(t, &out, slowStatement)

	failed := handle.Model(&loggedEntry{}).Select("id").Where("value = ?", scanIdleValue)
	_ = failed.AddError(errors.New("an earlier statement on this handle was refused"))
	var ids []uint
	if err := failed.Scan(&ids).Error; err == nil {
		t.Fatal("a Scan on a handle that carries an error reported none")
	}

	written := out.String()
	if strings.Contains(written, "withheld") {
		t.Errorf("the output says a statement was withheld, and none was built:\n%s", written)
	}
	lines := strings.Split(strings.TrimSuffix(written, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("the Scan was written as %d lines, want one:\n%s", len(lines), written)
	}
	ending := regexp.MustCompile(` \[error\] statement failed \[\d+\.\d{3}ms\] \[rows:0\] *$`)
	if !strings.Contains(lines[0], "/logger_test.go:") || !ending.MatchString(lines[0]) {
		t.Errorf("the line does not name this file as the call site and end with the failure, its time and "+
			"its rows:\n%s", lines[0])
	}
}

// TestTheLoggerLeavesGORMsRecorderFilterAlone holds the rule that this package
// sets no GORM package variable: the Scan statements are recognised inside the
// logger, not by a change to what every handle in the process shares.
// logger.RecorderParamsFilter is the variable that would tempt. It is how the
// recorder each Scan runs on, a host's own handles' included, answers GORM's
// request for the values a statement binds, and by default it hands them back.
// A package that set it would take the values out of what every one of those
// handles logs without a word, and a host that set it too would undo it. The
// filter is called with a statement and one parameter once the package is
// loaded, and again after the logger has written statements run through Scan
// and a handle has been opened the way the module opens every one; each time it
// must hand the parameter back unchanged.
func TestTheLoggerLeavesGORMsRecorderFilterAlone(t *testing.T) {
	handsBack := func(when string) bool {
		t.Helper()
		if logger.RecorderParamsFilter == nil {
			t.Errorf("logger.RecorderParamsFilter is nil %s, and GORM's own hands the parameters back", when)
			return false
		}
		statement, params := logger.RecorderParamsFilter(context.Background(), "SELECT ?", filterValue)
		if statement != "SELECT ?" || len(params) != 1 || params[0] != filterValue {
			t.Errorf("logger.RecorderParamsFilter %s gives (%q, %v), want the statement and its parameter back "+
				"unchanged", when, statement, params)
			return false
		}
		return true
	}
	if !handsBack("once the package is loaded") {
		return
	}

	var out bytes.Buffer
	handle := loggingOn(t, &out, time.Nanosecond).Debug()
	if err := handle.Create(&loggedEntry{ID: 1, Value: filterValue}).Error; err != nil {
		t.Fatalf("writing the row the statements meet: %v", err)
	}
	var id uint
	if err := handle.Raw("SELECT id FROM logged_entries WHERE value = ?", filterValue).Scan(&id).Error; err != nil {
		t.Fatalf("a scanned read failed: %v", err)
	}
	if err := handle.Raw("INSERT INTO logged_entries (id, value) VALUES (2, ?) RETURNING id",
		filterValue).Scan(&id).Error; err == nil {
		t.Fatal("a scanned insert under the unique value was written, want it refused")
	}
	if !strings.Contains(out.String(), withheldText) {
		t.Fatalf("the logger wrote no withheld statement, so the Scan path was not run:\n%s", out.String())
	}

	opened, err := openHandle(t.Context(), sqliteSpec(), sqliteSection(filepath.Join(t.TempDir(), "filter.db")))
	if err != nil {
		t.Fatalf("opening a handle the way the module opens every one: %v", err)
	}
	t.Cleanup(func() {
		if err := Close(opened); err != nil {
			t.Errorf("releasing the handle: %v", err)
		}
	})
	var echoed string
	if err := opened.Raw("SELECT ?", filterValue).Scan(&echoed).Error; err != nil {
		t.Fatalf("a scanned read on the opened handle failed: %v", err)
	}

	handsBack("after the logger has written statements run through Scan")
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
