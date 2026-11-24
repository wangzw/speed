package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

const (
	slowValue   = "logged-value-slow"
	failedValue = "logged-value-failed"
	debugValue  = "logged-value-debugged"

	scanFailedValue = "logged-value-scan-failed"
	scanSlowValue   = "logged-value-scan-slow"
	scanDebugValue  = "logged-value-scan-debugged"
	scanChainValue  = "logged-value-scan-chained"
	scanIdleValue   = "logged-value-scan-idle"
	finisherValue   = "logged-value-finisher"
	filterValue     = "logged-value-filter-probe"
	sessionValue    = "logged-value-session"
	silencedValue   = "logged-value-silenced"
)

// withheldText is written out here, not read from the logger, so rewording it is a change these cases see.
const withheldText = "statement withheld: GORM filled its values in before the logger saw it"

const notSlow = time.Hour

const briefly = time.Millisecond

func began(took time.Duration) time.Time { return time.Now().Add(-took) }

var thisFile = func() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}()

func nextLine() int {
	_, _, line, _ := runtime.Caller(1)
	return line + 1
}

// recordingHandler keeps a copy of each record and a trail of what it was asked.
// Handle does not judge the level, which is why the logger has to ask Enabled
// before it writes.
type recordingHandler struct {
	mu             sync.Mutex
	allows         func(slog.Level) bool
	kept           []slog.Record
	trail          []string
	asked, handled []context.Context
}

var _ slog.Handler = (*recordingHandler)(nil)

func (h *recordingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trail = append(h.trail, "enabled "+level.String())
	h.asked = append(h.asked, ctx)
	return h.allows == nil || h.allows(level)
}

func (h *recordingHandler) Handle(ctx context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trail = append(h.trail, "handle")
	h.handled = append(h.handled, ctx)
	h.kept = append(h.kept, record.Clone())
	return nil
}

func (*recordingHandler) WithAttrs([]slog.Attr) slog.Handler {
	panic("recordingHandler: no case binds attributes to the logger")
}

func (*recordingHandler) WithGroup(string) slog.Handler {
	panic("recordingHandler: no case opens a group on the logger")
}

func (h *recordingHandler) allowFrom(level slog.Level) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allows = func(asked slog.Level) bool { return asked >= level }
}

func (h *recordingHandler) written() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.kept)
}

func (h *recordingHandler) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.trail)
}

func (h *recordingHandler) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept, h.trail, h.asked, h.handled = nil, nil, nil, nil
}

func (h *recordingHandler) note(event string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trail = append(h.trail, event)
}

func (h *recordingHandler) statement(text string, rows int64) func() (string, int64) {
	return func() (string, int64) {
		h.note("render")
		return text, rows
	}
}

func (h *recordingHandler) probe() fmt.Stringer { return renderProbe{h} }

type renderProbe struct{ rec *recordingHandler }

func (p renderProbe) String() string {
	p.rec.note("render")
	return "probed"
}

// vocabulary is written out here and read from nowhere in the logger, so
// rewording a message, moving one to another level or renaming an attribute is a
// change these cases see.
var vocabulary = map[string]struct {
	level             slog.Level
	always, sometimes []string
}{
	"statement failed": {slog.LevelError, []string{"elapsed", "caller"}, []string{"classes", "rows", "statement"}},
	"slow statement":   {slog.LevelWarn, []string{"threshold", "elapsed", "caller"}, []string{"rows", "statement"}},
	"statement":        {slog.LevelInfo, []string{"elapsed", "caller"}, []string{"rows", "statement"}},
	"gorm info":        {slog.LevelInfo, []string{"detail", "caller"}, nil},
	"gorm warning":     {slog.LevelWarn, []string{"detail", "caller"}, nil},
	"gorm error":       {slog.LevelError, []string{"detail", "caller"}, nil},
}

var attributeKinds = map[string]slog.Kind{
	"classes":   slog.KindString,
	"threshold": slog.KindDuration,
	"elapsed":   slog.KindDuration,
	"rows":      slog.KindInt64,
	"statement": slog.KindString,
	"caller":    slog.KindString,
	"detail":    slog.KindString,
}

// withoutSlowStatements leaves out the "slow statement" records, whose presence
// depends on the machine's load. It only narrows a count; no assertion about what
// a record carries reads its result.
func withoutSlowStatements(records []slog.Record) []slog.Record {
	kept := make([]slog.Record, 0, len(records))
	for _, record := range records {
		if record.Message == "slow statement" {
			continue
		}
		kept = append(kept, record)
	}
	return kept
}

func attributesOf(t *testing.T, record slog.Record) map[string]slog.Value {
	t.Helper()
	attrs := make(map[string]slog.Value, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		if _, again := attrs[attr.Key]; again {
			t.Errorf("the %q record carries the attribute %q more than once", record.Message, attr.Key)
		}
		if attr.Value.Kind() == slog.KindGroup {
			t.Errorf("the %q record carries %q as a group, and the logger writes its attributes flat",
				record.Message, attr.Key)
		}
		attrs[attr.Key] = attr.Value
		return true
	})
	return attrs
}

func everything(record slog.Record) string {
	var text strings.Builder
	text.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		text.WriteString(" " + attr.Key + "=" + attr.Value.String())
		return true
	})
	return text.String()
}

func textOf(attrs map[string]slog.Value, key string) (string, bool) {
	value, carried := attrs[key]
	if !carried || value.Kind() != slog.KindString {
		return "", false
	}
	return value.String(), true
}

func durationOf(attrs map[string]slog.Value, key string) (time.Duration, bool) {
	value, carried := attrs[key]
	if !carried || value.Kind() != slog.KindDuration {
		return 0, false
	}
	return value.Duration(), true
}

func countOf(attrs map[string]slog.Value, key string) (int64, bool) {
	value, carried := attrs[key]
	if !carried || value.Kind() != slog.KindInt64 {
		return 0, false
	}
	return value.Int64(), true
}

func siteOf(record slog.Record) runtime.Frame {
	if record.PC == 0 {
		return runtime.Frame{}
	}
	frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
	return frame
}

func checkVocabulary(t *testing.T, record slog.Record) map[string]slog.Value {
	t.Helper()
	attrs := attributesOf(t, record)
	kind, known := vocabulary[record.Message]
	if !known {
		t.Errorf("the logger wrote the message %q, which is none of its own words: %s", record.Message,
			everything(record))
		return attrs
	}
	if record.Level != kind.level {
		t.Errorf("the %q record is written at %s, want %s", record.Message, record.Level, kind.level)
	}
	if record.Time.IsZero() {
		t.Errorf("the %q record carries no time", record.Message)
	}
	for _, key := range kind.always {
		if _, carried := attrs[key]; !carried {
			t.Errorf("the %q record does not carry %q: %s", record.Message, key, everything(record))
		}
	}
	for key, value := range attrs {
		if !slices.Contains(kind.always, key) && !slices.Contains(kind.sometimes, key) {
			t.Errorf("the %q record carries the attribute %q, which is none of its kind's: %s", record.Message, key,
				everything(record))
			continue
		}
		if want := attributeKinds[key]; value.Kind() != want {
			t.Errorf("the %q record carries %q as a %s, want a %s", record.Message, key, value.Kind(), want)
		}
	}
	if elapsed, carried := durationOf(attrs, "elapsed"); carried && elapsed <= 0 {
		t.Errorf("the %q record carries an elapsed time of %v, want a positive duration", record.Message, elapsed)
	}
	if statement, carried := attrs["statement"]; carried && statement.String() == "" {
		t.Errorf("the %q record carries an empty statement, which is left out when none was built", record.Message)
	}
	checkCallSite(t, record, attrs)
	return attrs
}

func checkCallSite(t *testing.T, record slog.Record, attrs map[string]slog.Value) {
	t.Helper()
	frame := siteOf(record)
	caller, _ := textOf(attrs, "caller")
	switch {
	case record.PC == 0:
		t.Errorf("the %q record carries no program counter, so a handler that adds the source reports no call "+
			"site", record.Message)
	case filepath.Base(frame.File) == "logger.go" || strings.Contains(caller, "/logger.go:"):
		t.Errorf("the %q record names the logger as its call site: the program counter resolves to %s:%d and "+
			"the caller attribute is %q", record.Message, frame.File, frame.Line, caller)
	case frame.File != thisFile:
		t.Errorf("the %q record's program counter resolves to %s:%d in %s, want a line of %s, the file the call "+
			"was made from", record.Message, frame.File, frame.Line, frame.Function, thisFile)
	}
	if want := thisFile + ":" + strconv.Itoa(frame.Line); caller != want {
		t.Errorf("the %q record's caller attribute is %q, want %q, the program counter's own site", record.Message,
			caller, want)
	}
}

func expectRecord(t *testing.T, record slog.Record, message string, line int) map[string]slog.Value {
	t.Helper()
	attrs := checkVocabulary(t, record)
	if record.Message != message {
		t.Errorf("the record says %q, want %q: %s", record.Message, message, everything(record))
	}
	if line != 0 {
		if want := thisFile + ":" + strconv.Itoa(line); attrs["caller"].String() != want {
			t.Errorf("the %q record names the call site %s, want %s, the line of the call that caused it",
				record.Message, attrs["caller"], want)
		}
	}
	return attrs
}

func assertNoneCarries(t *testing.T, records []slog.Record, values ...string) {
	t.Helper()
	for _, record := range records {
		text := everything(record)
		for _, value := range values {
			if strings.Contains(text, value) {
				t.Errorf("a record carries the bound value %q: %s", value, text)
			}
		}
	}
}

type loggedEntry struct {
	ID    uint
	Value string
}

func (loggedEntry) TableName() string { return "logged_entries" }

func loggingOn(t *testing.T, rec *recordingHandler, slow time.Duration) *gorm.DB {
	t.Helper()
	statements := newStatementLogger(slog.New(rec))
	statements.slow = slow
	handle := dbsuite.OpenSQLite(t).Session(&gorm.Session{Logger: statements})
	if err := handle.Exec("CREATE TABLE logged_entries (id INTEGER PRIMARY KEY, value TEXT NOT NULL UNIQUE)").Error; err != nil {
		t.Fatalf("creating logged_entries: %v", err)
	}
	rec.reset()
	return handle
}

func directLogger(rec *recordingHandler, level logger.LogLevel, slow time.Duration) logger.Interface {
	statements := newStatementLogger(slog.New(rec))
	statements.slow = slow
	return statements.LogMode(level)
}

// emission is one way GORM reaches the logger, with the record written for it;
// the six cover every message and all four methods GORM calls.
type emission struct {
	name            string
	message         string
	level           slog.Level
	needle, carrier string
	issue           func(ctx context.Context, l logger.Interface, rec *recordingHandler)
}

const (
	emissionThreshold = time.Hour
	emissionTook      = 2 * time.Hour
)

var emissions = []emission{
	{
		name: "an informational message of GORM's", message: "gorm info", level: slog.LevelInfo,
		needle: "info-text", carrier: "detail",
		issue: func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
			l.Info(ctx, "info-text %v", rec.probe())
		},
	},
	{
		name: "a warning of GORM's", message: "gorm warning", level: slog.LevelWarn,
		needle: "warning-text", carrier: "detail",
		issue: func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
			l.Warn(ctx, "warning-text %v", rec.probe())
		},
	},
	{
		name: "an error message of GORM's", message: "gorm error", level: slog.LevelError,
		needle: "error-text", carrier: "detail",
		issue: func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
			l.Error(ctx, "error-text %v", rec.probe())
		},
	},
	{
		name: "a slow statement", message: "slow statement", level: slog.LevelWarn,
		needle: "slow-text", carrier: "statement",
		issue: func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
			l.Trace(ctx, began(emissionTook), rec.statement("SELECT slow-text", 1), nil)
		},
	},
	{
		name: "a failed statement", message: "statement failed", level: slog.LevelError,
		needle: "failed-text", carrier: "statement",
		issue: func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
			l.Trace(ctx, began(briefly), rec.statement("SELECT failed-text", 0), errors.New("refused"))
		},
	},
	{
		name: "a statement that ran as any other", message: "statement", level: slog.LevelInfo,
		needle: "plain-text", carrier: "statement",
		issue: func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
			l.Trace(ctx, began(briefly), rec.statement("SELECT plain-text", 3), nil)
		},
	},
}

// TestEveryKindOfRecordCarriesExactlyItsAttributes holds each message to its level, attribute set
// and attribute kinds.
func TestEveryKindOfRecordCarriesExactlyItsAttributes(t *testing.T) {
	refused := fmt.Errorf("%w: %w", gorm.ErrDuplicatedKey, quotingError{failedValue, "23505"})
	cases := []struct {
		name    string
		message string
		issue   func(ctx context.Context, l logger.Interface, rec *recordingHandler)
		keys    []string
	}{
		{
			"a failed statement whose error carries classes", "statement failed",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(time.Second), rec.statement("SELECT 1", 7), refused)
			},
			[]string{"caller", "classes", "elapsed", "rows", "statement"},
		},
		{
			"a failed statement whose error carries none", "statement failed",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(time.Second), rec.statement("SELECT 1", 7), errors.New("refused"))
			},
			[]string{"caller", "elapsed", "rows", "statement"},
		},
		{
			"a failed statement with no statement built and no rows counted", "statement failed",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(time.Second), rec.statement("", -1), errors.New("refused"))
			},
			[]string{"caller", "elapsed"},
		},
		{
			"a slow statement", "slow statement",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(emissionTook), rec.statement("SELECT 1", 7), nil)
			},
			[]string{"caller", "elapsed", "rows", "statement", "threshold"},
		},
		{
			"a statement that ran as any other", "statement",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(briefly), rec.statement("SELECT 1", 7), nil)
			},
			[]string{"caller", "elapsed", "rows", "statement"},
		},
		{
			"an informational message of GORM's", "gorm info",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Info(ctx, "a message about %v", rec.probe())
			},
			[]string{"caller", "detail"},
		},
		{
			"a warning of GORM's", "gorm warning",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Warn(ctx, "a message about %v", rec.probe())
			},
			[]string{"caller", "detail"},
		},
		{
			"an error message of GORM's", "gorm error",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Error(ctx, "a message about %v", rec.probe())
			},
			[]string{"caller", "detail"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			c.issue(t.Context(), directLogger(rec, logger.Info, emissionThreshold), rec)

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("%d records were written, want one", len(records))
			}
			attrs := expectRecord(t, records[0], c.message, 0)
			if got := slices.Sorted(maps.Keys(attrs)); !slices.Equal(got, c.keys) {
				t.Errorf("the record carries the attributes %v, want %v: %s", got, c.keys, everything(records[0]))
			}
		})
	}
}

// TestTheLoggerWritesNoValueOnAnyPath checks that no value a statement binds or reads reaches a
// record on any of the three write paths.
func TestTheLoggerWritesNoValueOnAnyPath(t *testing.T) {
	rec := &recordingHandler{}
	handle := loggingOn(t, rec, time.Nanosecond)
	debugging := loggingOn(t, rec, notSlow).Debug()

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

	records := rec.written()
	assertNoneCarries(t, records, slowValue, failedValue, debugValue)
	want := []struct {
		message, classes, statement string
	}{
		{"slow statement", "", "VALUES (?,?)"},
		{"statement failed", "result code 2067", "VALUES (?,?)"},
		{"statement", "", "value = ?"},
		{"statement failed", "record not found", "value = ?"},
	}
	if len(records) != len(want) {
		t.Fatalf("%d records were written, want %d:\n%v", len(records), len(want), everythingOf(records))
	}
	for i, w := range want {
		attrs := expectRecord(t, records[i], w.message, 0)
		if classes, _ := textOf(attrs, "classes"); classes != w.classes {
			t.Errorf("the %q record carries the classes %q, want %q", w.message, classes, w.classes)
		}
		if statement, _ := textOf(attrs, "statement"); !strings.Contains(statement, w.statement) {
			t.Errorf("the %q record carries the statement %q, want one with %q in it", w.message, statement,
				w.statement)
		}
	}
	if threshold, _ := durationOf(attributesOf(t, records[0]), "threshold"); threshold != time.Nanosecond {
		t.Errorf("the slow statement carries the threshold %v, want the 1ns it was run at", threshold)
	}
}

func everythingOf(records []slog.Record) string {
	lines := make([]string, len(records))
	for i, record := range records {
		lines[i] = everything(record)
	}
	return strings.Join(lines, "\n")
}

// TestAStatementRunThroughScanIsWrittenWithoutItsText checks that a statement the Scan finisher
// runs is written with its text withheld on every path.
func TestAStatementRunThroughScanIsWrittenWithoutItsText(t *testing.T) {
	cases := []struct {
		name             string
		value            string
		slow             time.Duration
		debug            bool
		issue            func(handle *gorm.DB, value string) error
		fails            bool
		message, classes string
		threshold        time.Duration
		rows             int64
		rowsKnown        bool
	}{
		{
			name: "failed", value: scanFailedValue, slow: notSlow, fails: true,
			issue: func(handle *gorm.DB, value string) error {
				var id uint
				return handle.Raw("INSERT INTO logged_entries (id, value) VALUES (2, ?) RETURNING id", value).Scan(&id).Error
			},
			message: "statement failed", classes: "result code 2067",
		},
		{
			name: "slow", value: scanSlowValue, slow: time.Nanosecond,
			issue: func(handle *gorm.DB, value string) error {
				var id uint
				return handle.Raw("SELECT id FROM logged_entries WHERE value = ?", value).Scan(&id).Error
			},
			message: "slow statement", threshold: time.Nanosecond, rows: 1, rowsKnown: true,
		},
		{
			name: "debugged", value: scanDebugValue, slow: notSlow, debug: true,
			issue: func(handle *gorm.DB, value string) error {
				var id uint
				return handle.Raw("SELECT id FROM logged_entries WHERE value = ?", value).Scan(&id).Error
			},
			message: "statement", rows: 1, rowsKnown: true,
		},
		{
			name: "chained", value: scanChainValue, slow: notSlow, debug: true,
			issue: func(handle *gorm.DB, value string) error {
				var ids []uint
				return handle.Model(&loggedEntry{}).Select("id").Where("value = ?", value).Scan(&ids).Error
			},
			message: "statement", rows: 1, rowsKnown: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			handle := loggingOn(t, rec, c.slow)
			if err := handle.Create(&loggedEntry{ID: 1, Value: c.value}).Error; err != nil {
				t.Fatalf("writing the row the statement meets: %v", err)
			}
			if c.debug {
				handle = handle.Debug()
			}
			rec.reset()
			if err := c.issue(handle, c.value); (err != nil) != c.fails {
				t.Fatalf("the statement reported %v, want a failure: %t", err, c.fails)
			}

			records := rec.written()
			assertNoneCarries(t, records, c.value)
			if len(records) != 1 {
				t.Fatalf("the statement was written as %d records, want one:\n%s", len(records), everythingOf(records))
			}
			attrs := expectRecord(t, records[0], c.message, 0)
			if statement, _ := textOf(attrs, "statement"); statement != withheldText {
				t.Errorf("the record carries the statement %q, want the withheld text %q", statement, withheldText)
			}
			if classes, _ := textOf(attrs, "classes"); classes != c.classes {
				t.Errorf("the record carries the classes %q, want %q", classes, c.classes)
			}
			if threshold, _ := durationOf(attrs, "threshold"); threshold != c.threshold {
				t.Errorf("the record carries the threshold %v, want %v", threshold, c.threshold)
			}
			if rows, counted := countOf(attrs, "rows"); c.rowsKnown && (!counted || rows != c.rows) {
				t.Errorf("the record counts %d rows (counted: %t), want %d", rows, counted, c.rows)
			}
		})
	}
}

// TestAStatementRunThroughAnotherFinisherKeepsItsTextWhereScanWithholdsIt checks that Row, Rows and
// the generic Scan leave the text in place, with its placeholders unfilled.
func TestAStatementRunThroughAnotherFinisherKeepsItsTextWhereScanWithholdsIt(t *testing.T) {
	const statement = "SELECT id FROM logged_entries WHERE value = ?"
	cases := []struct {
		name      string
		run       func(t *testing.T, handle *gorm.DB, value string)
		uncounted bool
	}{
		{
			name: "Row",
			run: func(t *testing.T, handle *gorm.DB, value string) {
				t.Helper()
				var id uint
				if err := handle.Raw(statement, value).Row().Scan(&id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("looking up a value no row has gave %v, want no row", err)
				}
			},
			uncounted: true,
		},
		{
			name: "Rows",
			run: func(t *testing.T, handle *gorm.DB, value string) {
				t.Helper()
				rows, err := handle.Raw(statement, value).Rows()
				if err != nil {
					t.Fatalf("looking up a value no row has failed: %v", err)
				}
				defer func() {
					if err := rows.Close(); err != nil {
						t.Errorf("closing the rows: %v", err)
					}
				}()
				if rows.Next() {
					t.Fatal("looking up a value no row has found one")
				}
				if err := rows.Err(); err != nil {
					t.Fatalf("reading the rows: %v", err)
				}
			},
			uncounted: true,
		},
		{
			name: "the generic interface's Scan",
			run: func(t *testing.T, handle *gorm.DB, value string) {
				t.Helper()
				var ids []uint
				if err := gorm.G[loggedEntry](handle).Raw(statement, value).Scan(t.Context(), &ids); err != nil {
					t.Fatalf("looking up a value no row has failed: %v", err)
				}
			},
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value := fmt.Sprintf("%s-%d", finisherValue, i)
			rec := &recordingHandler{}
			handle := loggingOn(t, rec, notSlow).Debug()

			c.run(t, handle, value)

			records := rec.written()
			assertNoneCarries(t, records, value)
			if len(records) != 1 {
				t.Fatalf("the statement was written as %d records, want one:\n%s", len(records), everythingOf(records))
			}
			attrs := expectRecord(t, records[0], "statement", 0)
			if got, _ := textOf(attrs, "statement"); got != statement {
				t.Errorf("the record carries the statement %q, want the statement with its placeholder", got)
			}
			if rows, counted := countOf(attrs, "rows"); c.uncounted && counted {
				t.Errorf("the record counts %d rows for a statement GORM counted none for", rows)
			}
		})
	}
}

// TestAScanThatRanNoStatementIsNotWrittenAsWithheld checks that a Scan that built no statement is
// written without a statement attribute and without the withheld note.
func TestAScanThatRanNoStatementIsNotWrittenAsWithheld(t *testing.T) {
	rec := &recordingHandler{}
	handle := loggingOn(t, rec, notSlow)

	failed := handle.Model(&loggedEntry{}).Select("id").Where("value = ?", scanIdleValue)
	_ = failed.AddError(errors.New("an earlier statement on this handle was refused"))
	var ids []uint
	if err := failed.Scan(&ids).Error; err == nil {
		t.Fatal("a Scan on a handle that carries an error reported none")
	}

	records := rec.written()
	assertNoneCarries(t, records, "withheld", scanIdleValue)
	if len(records) != 1 {
		t.Fatalf("the Scan was written as %d records, want one:\n%s", len(records), everythingOf(records))
	}
	attrs := expectRecord(t, records[0], "statement failed", 0)
	if statement, carried := attrs["statement"]; carried {
		t.Errorf("the record carries the statement %q, and none was built", statement)
	}
	if classes, carried := attrs["classes"]; carried {
		t.Errorf("the record carries the classes %q, and the error carries none", classes)
	}
	if rows, counted := countOf(attrs, "rows"); !counted || rows != 0 {
		t.Errorf("the record counts %d rows (counted: %t), want the 0 GORM reports", rows, counted)
	}
}

// TestTheLoggerLeavesGORMsRecorderFilterAlone checks that logger.RecorderParamsFilter still hands
// the parameter back unchanged.
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

	rec := &recordingHandler{}
	handle := loggingOn(t, rec, time.Nanosecond).Debug()
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
	withheld := 0
	for _, record := range rec.written() {
		if statement, _ := textOf(attributesOf(t, record), "statement"); statement == withheldText {
			withheld++
		}
	}
	if withheld == 0 {
		t.Fatalf("the logger wrote no withheld statement, so the Scan path was not run:\n%s",
			everythingOf(rec.written()))
	}

	opened, err := openHandle(t.Context(), sqliteSpec(), sqliteSection(filepath.Join(t.TempDir(), "filter.db")),
		discardingLogger())
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
// of a malformed input does, and which carries an engine's SQLSTATE.
type quotingError struct{ value, state string }

func (e quotingError) Error() string    { return fmt.Sprintf("invalid input syntax: %q", e.value) }
func (e quotingError) SQLState() string { return e.state }

// resultCodeError is an error whose text quotes a value, as SQLite's refusal of
// a duplicate does, and which carries the engine's numeric result code.
type resultCodeError struct {
	value string
	code  int
}

func (e resultCodeError) Error() string { return fmt.Sprintf("UNIQUE constraint failed: %q", e.value) }
func (e resultCodeError) Code() int     { return e.code }

// TestAFailedStatementIsWrittenWithTheClassesOfItsError checks that a failed statement writes the
// error's classes, spelled as documented, and never its text.
func TestAFailedStatementIsWrittenWithTheClassesOfItsError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		classes string
	}{
		{"an engine's SQLSTATE", quotingError{failedValue, "22P02"}, "SQLSTATE 22P02"},
		{"an engine's result code", resultCodeError{failedValue, 2067}, "result code 2067"},
		{
			"a duplicate key", fmt.Errorf("writing %s: %w", failedValue, gorm.ErrDuplicatedKey),
			"duplicated key not allowed",
		},
		{
			"a duplicate key with a SQLSTATE",
			fmt.Errorf("%w: %w", gorm.ErrDuplicatedKey, quotingError{failedValue, "23505"}),
			"duplicated key not allowed, SQLSTATE 23505",
		},
		{
			"a duplicate key with a result code",
			fmt.Errorf("%w: %w", gorm.ErrDuplicatedKey, resultCodeError{failedValue, 2067}),
			"duplicated key not allowed, result code 2067",
		},
		{
			"a foreign key violation with a SQLSTATE",
			fmt.Errorf("%w: %w", gorm.ErrForeignKeyViolated, quotingError{failedValue, "23503"}),
			"violates foreign key constraint, SQLSTATE 23503",
		},
		{"a lookup that found no row", fmt.Errorf("%s: %w", failedValue, gorm.ErrRecordNotFound), "record not found"},
		{"a cancelled context", fmt.Errorf("%s: %w", failedValue, context.Canceled), "context canceled"},
		{
			"an exceeded deadline", fmt.Errorf("%s: %w", failedValue, context.DeadlineExceeded),
			"context deadline exceeded",
		},
		{"an error that carries no class", errors.New(failedValue), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			l := directLogger(rec, logger.Warn, slowStatement)
			l.Trace(t.Context(), began(briefly), rec.statement("SELECT ?", -1), c.err)

			records := rec.written()
			assertNoneCarries(t, records, failedValue)
			if len(records) != 1 {
				t.Fatalf("the failure %v was written as %d records, want one", c.err, len(records))
			}
			attrs := expectRecord(t, records[0], "statement failed", 0)
			classes, carried := textOf(attrs, "classes")
			if c.classes == "" && carried {
				t.Errorf("the failure %v is written with the classes %q, and it carries none", c.err, classes)
			}
			if c.classes != "" && classes != c.classes {
				t.Errorf("the failure %v is written with the classes %q (carried: %t), want %q", c.err, classes,
					carried, c.classes)
			}
			if statement, _ := textOf(attrs, "statement"); statement != "SELECT ?" {
				t.Errorf("the failure %v is written with the statement %q, want its placeholder text", c.err, statement)
			}
		})
	}
}

// TestTheStatementAndGORMsTextAreAttributesNeverTheMessage checks that a statement's text and
// GORM's formatted text stay in their attributes, never in the message.
func TestTheStatementAndGORMsTextAreAttributesNeverTheMessage(t *testing.T) {
	for _, e := range emissions {
		t.Run(e.name, func(t *testing.T) {
			rec := &recordingHandler{}
			e.issue(t.Context(), directLogger(rec, logger.Info, emissionThreshold), rec)

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("%d records were written, want one", len(records))
			}
			attrs := expectRecord(t, records[0], e.message, 0)
			if strings.Contains(records[0].Message, e.needle) {
				t.Errorf("the message %q carries the text %q", records[0].Message, e.needle)
			}
			if text, _ := textOf(attrs, e.carrier); !strings.Contains(text, e.needle) {
				t.Errorf("the %s attribute is %q, want it to carry %q", e.carrier, text, e.needle)
			}
			for key, value := range attrs {
				if key != e.carrier && strings.Contains(value.String(), e.needle) {
					t.Errorf("the %s attribute carries %q as well as the %s attribute: %q", key, e.needle, e.carrier,
						value)
				}
			}
		})
	}
}

// TestElapsedIsTheTimeTheStatementTook checks that elapsed is a positive duration matching the time
// since the statement began.
func TestElapsedIsTheTimeTheStatementTook(t *testing.T) {
	const took = 90 * time.Second
	cases := []struct {
		name    string
		message string
		err     error
	}{
		{"a slow statement", "slow statement", nil},
		{"a failed statement", "statement failed", errors.New("refused")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			directLogger(rec, logger.Warn, time.Second).Trace(t.Context(), began(took), rec.statement("SELECT 1", 1), c.err)

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("%d records were written, want one", len(records))
			}
			attrs := expectRecord(t, records[0], c.message, 0)
			elapsed, carried := durationOf(attrs, "elapsed")
			if !carried {
				t.Fatalf("the record carries no elapsed duration: %s", everything(records[0]))
			}
			if elapsed < took || elapsed > took+time.Minute {
				t.Errorf("the record carries an elapsed time of %v for a statement begun %v ago", elapsed, took)
			}
		})
	}

	t.Run("a statement that ran as any other", func(t *testing.T) {
		rec := &recordingHandler{}
		directLogger(rec, logger.Info, time.Hour).Trace(t.Context(), began(took), rec.statement("SELECT 1", 1), nil)

		records := rec.written()
		if len(records) != 1 {
			t.Fatalf("%d records were written, want one", len(records))
		}
		attrs := expectRecord(t, records[0], "statement", 0)
		if elapsed, _ := durationOf(attrs, "elapsed"); elapsed < took || elapsed > took+time.Minute {
			t.Errorf("the record carries an elapsed time of %v for a statement begun %v ago", elapsed, took)
		}
	})
}

// TestASlowStatementCarriesTheThresholdItExceeded checks that the threshold attribute is the
// logger's own, and that only statements past it are written slow.
func TestASlowStatementCarriesTheThresholdItExceeded(t *testing.T) {
	t.Run("the threshold the logger runs at", func(t *testing.T) {
		rec := &recordingHandler{}
		l := directLogger(rec, logger.Warn, emissionThreshold)
		l.Trace(t.Context(), began(emissionTook), rec.statement("SELECT slow", 1), nil)
		l.Trace(t.Context(), began(10*time.Millisecond), rec.statement("SELECT quick", 1), nil)

		records := rec.written()
		if len(records) != 1 {
			t.Fatalf("%d records were written for a slow statement and a quick one, want the slow one's:\n%s",
				len(records), everythingOf(records))
		}
		attrs := expectRecord(t, records[0], "slow statement", 0)
		if threshold, _ := durationOf(attrs, "threshold"); threshold != emissionThreshold {
			t.Errorf("the record carries the threshold %v, want the %v the logger runs at", threshold, emissionThreshold)
		}
	})

	t.Run("a statement that failed past the threshold", func(t *testing.T) {
		rec := &recordingHandler{}
		l := directLogger(rec, logger.Warn, emissionThreshold)
		l.Trace(t.Context(), began(emissionTook), rec.statement("SELECT failed", 1), errors.New("refused"))

		records := rec.written()
		if len(records) != 1 {
			t.Fatalf("%d records were written for a statement that failed and was slow, want one:\n%s", len(records),
				everythingOf(records))
		}
		attrs := expectRecord(t, records[0], "statement failed", 0)
		if threshold, carried := attrs["threshold"]; carried {
			t.Errorf("the failure carries the threshold %v, and a failure is no slow statement", threshold)
		}
	})

	t.Run("the threshold a logger is built with", func(t *testing.T) {
		rec := &recordingHandler{}
		newStatementLogger(slog.New(rec)).Trace(t.Context(), began(emissionTook), rec.statement("SELECT slow", 1), nil)

		records := rec.written()
		if len(records) != 1 {
			t.Fatalf("%d records were written for a statement that took far longer than the threshold, want one:\n%s", len(records),
				everythingOf(records))
		}
		attrs := expectRecord(t, records[0], "slow statement", 0)
		if threshold, _ := durationOf(attrs, "threshold"); threshold != 200*time.Millisecond {
			t.Errorf("the record carries the threshold %v, want the 200ms of GORM's default logger", threshold)
		}
	})
}

// TestRowsAreLeftOutWhenGORMCountedNone checks that rows is the reported count and is absent only
// when GORM reports -1.
func TestRowsAreLeftOutWhenGORMCountedNone(t *testing.T) {
	cases := []struct {
		name    string
		level   logger.LogLevel
		took    time.Duration
		err     error
		message string
	}{
		{"a failed statement", logger.Warn, briefly, errors.New("refused"), "statement failed"},
		{"a slow statement", logger.Warn, emissionTook, nil, "slow statement"},
		{"a statement that ran as any other", logger.Info, briefly, nil, "statement"},
	}
	for _, c := range cases {
		for _, counted := range []struct {
			rows int64
			want bool
		}{{-1, false}, {0, true}, {1, true}, {7, true}} {
			t.Run(fmt.Sprintf("%s/%d", c.name, counted.rows), func(t *testing.T) {
				rec := &recordingHandler{}
				directLogger(rec, c.level, emissionThreshold).Trace(t.Context(), began(c.took),
					rec.statement("SELECT 1", counted.rows), c.err)

				records := rec.written()
				if len(records) != 1 {
					t.Fatalf("%d records were written, want one", len(records))
				}
				attrs := expectRecord(t, records[0], c.message, 0)
				rows, carried := countOf(attrs, "rows")
				switch {
				case counted.want && (!carried || rows != counted.rows):
					t.Errorf("GORM counted %d rows, and the record carries %d (carried: %t)", counted.rows, rows, carried)
				case !counted.want && carried:
					t.Errorf("GORM counted no rows, and the record carries a count of %d", rows)
				}
			})
		}
	}
}

// TestGORMsOwnMessagesGoToTheDetailAttribute checks that GORM's text, formatted with its data and
// stripped of the trailing newline, goes to detail.
func TestGORMsOwnMessagesGoToTheDetailAttribute(t *testing.T) {
	cases := []struct {
		name    string
		message string
		issue   func(ctx context.Context, l logger.Interface)
		detail  string
	}{
		{
			"information formatted with its data, and its newline", "gorm info",
			func(ctx context.Context, l logger.Interface) {
				l.Info(ctx, "replacing callback `%s` from %s\n", "gorm:create", "callbacks.go:12")
			},
			"replacing callback `gorm:create` from callbacks.go:12",
		},
		{
			"a warning with a count in it, and no newline", "gorm warning",
			func(ctx context.Context, l logger.Interface) { l.Warn(ctx, "%d rows were skipped", 3) },
			"3 rows were skipped",
		},
		{
			"an error with an error among its data", "gorm error",
			func(ctx context.Context, l logger.Interface) {
				l.Error(ctx, "failed to parse value %#v, got error %v", "widgets", errors.New("unsupported data type"))
			},
			`failed to parse value "widgets", got error unsupported data type`,
		},
		{
			"information with no data", "gorm info",
			func(ctx context.Context, l logger.Interface) { l.Info(ctx, "nothing to format in this one") },
			"nothing to format in this one",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			c.issue(t.Context(), directLogger(rec, logger.Info, time.Minute))

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("%d records were written, want one", len(records))
			}
			attrs := expectRecord(t, records[0], c.message, 0)
			if detail, _ := textOf(attrs, "detail"); detail != c.detail {
				t.Errorf("the record carries the detail %q, want %q", detail, c.detail)
			}
			if got := slices.Sorted(maps.Keys(attrs)); !slices.Equal(got, []string{"caller", "detail"}) {
				t.Errorf("the record carries the attributes %v, want the detail and the call site", got)
			}
		})
	}
}

// TestGORMsOwnMessagesReachTheLoggerFromInsideGORM checks that a callback table's three messages
// arrive at their levels, with the call site outside GORM.
func TestGORMsOwnMessagesReachTheLoggerFromInsideGORM(t *testing.T) {
	rec := &recordingHandler{}
	root, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "messages.db")), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               directLogger(rec, logger.Info, notSlow),
	})
	if err != nil {
		t.Fatalf("opening a handle whose logger is the statement logger: %v", err)
	}
	t.Cleanup(func() {
		if err := Close(root); err != nil {
			t.Errorf("releasing the handle: %v", err)
		}
	})
	rec.reset()

	noop := func(*gorm.DB) {}
	processor := root.Callback().Create()
	replaced := nextLine()
	if err := processor.Replace("probe:replaced", noop); err != nil {
		t.Fatalf("replacing a callback: %v", err)
	}
	removed := nextLine()
	if err := processor.Remove("probe:removed"); err != nil {
		t.Fatalf("removing a callback: %v", err)
	}
	if err := processor.Before("probe:b").Register("probe:a", noop); err != nil {
		t.Fatalf("registering the first of two callbacks that name each other: %v", err)
	}
	conflicting := nextLine()
	if err := processor.Before("probe:a").Register("probe:b", noop); err == nil {
		t.Fatal("registering the second of two callbacks that name each other before themselves reported no error")
	}

	records := rec.written()
	if len(records) != 3 {
		t.Fatalf("GORM's three messages were written as %d records, want three:\n%s", len(records), everythingOf(records))
	}
	for i, c := range []struct {
		message string
		line    int
		detail  string
	}{
		{
			"gorm info", replaced,
			fmt.Sprintf("replacing callback `probe:replaced` from %s:%d", thisFile, replaced),
		},
		{
			"gorm warning", removed,
			fmt.Sprintf("removing callback `probe:removed` from %s:%d", thisFile, removed),
		},
		{
			"gorm error", conflicting,
			"Got error when compile callbacks, got conflicting callback probe:b with after probe:a",
		},
	} {
		attrs := expectRecord(t, records[i], c.message, c.line)
		if detail, _ := textOf(attrs, "detail"); detail != c.detail {
			t.Errorf("the %q record carries the detail %q, want %q", c.message, detail, c.detail)
		}
	}
}

// TestADebugSessionIsWrittenAsInfoRecords checks that a Debug session's statements are Info
// records, subject to the held logger's level.
func TestADebugSessionIsWrittenAsInfoRecords(t *testing.T) {
	session := func(t *testing.T, from slog.Level) []slog.Record {
		t.Helper()
		rec := &recordingHandler{}
		rec.allowFrom(from)
		debugging := loggingOn(t, rec, notSlow).Debug()

		if err := debugging.Create(&loggedEntry{ID: 1, Value: sessionValue}).Error; err != nil {
			t.Fatalf("writing the row: %v", err)
		}
		var found []loggedEntry
		if err := debugging.Where("value = ?", sessionValue).Find(&found).Error; err != nil {
			t.Fatalf("reading the row: %v", err)
		}
		if err := debugging.Exec("UPDATE logged_entries SET value = value WHERE id = ?", 1).Error; err != nil {
			t.Fatalf("updating the row: %v", err)
		}
		var id uint
		if err := debugging.Raw("SELECT id FROM logged_entries WHERE value = ?", sessionValue).Scan(&id).Error; err != nil {
			t.Fatalf("scanning the row: %v", err)
		}
		if err := debugging.Raw("SELECT id FROM logged_entries WHERE value = ?", sessionValue).Row().Scan(&id); err != nil {
			t.Fatalf("reading the row through Row: %v", err)
		}
		if err := debugging.Create(&loggedEntry{ID: 2, Value: sessionValue}).Error; err == nil {
			t.Fatal("a second row under the unique value was written, want it refused")
		}
		records := rec.written()
		assertNoneCarries(t, records, sessionValue)
		return records
	}

	t.Run("under the default level", func(t *testing.T) {
		records := session(t, slog.LevelInfo)
		want := []string{"statement", "statement", "statement", "statement", "statement", "statement failed"}
		if got := messagesOf(records); !slices.Equal(got, want) {
			t.Fatalf("the session was written as %v, want %v", got, want)
		}
		for _, record := range records {
			expectRecord(t, record, record.Message, 0)
			if record.Message == "statement" && record.Level != slog.LevelInfo {
				t.Errorf("a statement of the session is written at %s, want Info", record.Level)
			}
		}
	})

	t.Run("under a level above Info", func(t *testing.T) {
		records := session(t, slog.LevelWarn)
		if got, want := messagesOf(records), []string{"statement failed"}; !slices.Equal(got, want) {
			t.Fatalf("the session was written as %v, want the failure alone: %v", got, want)
		}
		expectRecord(t, records[0], "statement failed", 0)
	})
}

func messagesOf(records []slog.Record) []string {
	messages := make([]string, len(records))
	for i, record := range records {
		messages[i] = record.Message
	}
	return messages
}

// trailOf is the trail a handler keeps when the logger asks it about each wanted record, finds it written, renders it and hands it over: the level asked, the rendering and the delivery, in that order. Each wanted record is a level and its message, as "WARN slow statement".
func trailOf(wanted []string) []string {
	var trail []string
	for _, record := range wanted {
		level, _, _ := strings.Cut(record, " ")
		trail = append(trail, "enabled "+level, "render", "handle")
	}
	return trail
}

// TestTheLoggerWritesByItsLevel checks which records each GORM level writes, with GORM's level
// judged before the held logger is asked.
func TestTheLoggerWritesByItsLevel(t *testing.T) {
	cases := []struct {
		name    string
		level   logger.LogLevel
		written []string
	}{
		{"Silent", logger.Silent, nil},
		{"Error", logger.Error, []string{"ERROR gorm error", "ERROR statement failed"}},
		{
			"Warn", logger.Warn,
			[]string{"WARN gorm warning", "ERROR gorm error", "WARN slow statement", "ERROR statement failed"},
		},
		{"Info", logger.Info, []string{
			"INFO gorm info", "WARN gorm warning", "ERROR gorm error", "WARN slow statement", "ERROR statement failed",
			"INFO statement",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			l := directLogger(rec, c.level, emissionThreshold)
			for _, e := range emissions {
				e.issue(t.Context(), l, rec)
			}

			var got []string
			for _, record := range rec.written() {
				expectRecord(t, record, record.Message, 0)
				got = append(got, record.Level.String()+" "+record.Message)
			}
			if !slices.Equal(got, c.written) {
				t.Errorf("at level %s the logger wrote %v, want %v", c.name, got, c.written)
			}
			if trail, want := rec.events(), trailOf(c.written); !slices.Equal(trail, want) {
				t.Errorf("at level %s the handler was asked and handed %v, want %v: the held logger is asked only "+
					"about what GORM's level lets through", c.name, trail, want)
			}
		})
	}
}

// TestASilencedHandleWritesNothing checks that a Silent handle writes nothing and does not even ask
// the handler, while the same statements on an unsilenced handle are written.
func TestASilencedHandleWritesNothing(t *testing.T) {
	rec := &recordingHandler{}
	handle := loggingOn(t, rec, time.Nanosecond)
	statements := func(t *testing.T, handle *gorm.DB, id uint, value string) {
		t.Helper()
		if err := handle.Create(&loggedEntry{ID: id, Value: value}).Error; err != nil {
			t.Fatalf("writing a row: %v", err)
		}
		if err := handle.Create(&loggedEntry{ID: id + 1, Value: value}).Error; err == nil {
			t.Fatal("a second row under the unique value was written, want it refused")
		}
		if err := handle.Where("value = ?", "absent-"+value).First(&loggedEntry{}).Error; !errors.Is(err,
			gorm.ErrRecordNotFound) {
			t.Fatalf("looking up a value no row has gave %v, want no row", err)
		}
		var found uint
		if err := handle.Raw("SELECT id FROM logged_entries WHERE value = ?", value).Scan(&found).Error; err != nil {
			t.Fatalf("scanning a row: %v", err)
		}
		if err := handle.Raw("SELECT id FROM logged_entries WHERE value = ?", value).Row().Scan(&found); err != nil {
			t.Fatalf("reading a row through Row: %v", err)
		}
	}

	silenced := handle.Session(&gorm.Session{Logger: handle.Logger.LogMode(logger.Silent)})
	statements(t, silenced, 1, silencedValue)
	if records := rec.written(); len(records) != 0 || len(rec.events()) != 0 {
		t.Fatalf("a silenced handle wrote %d records and asked the handler %v:\n%s", len(records), rec.events(),
			everythingOf(records))
	}

	statements(t, handle, 11, silencedValue+"-control")
	records := rec.written()
	if len(records) == 0 {
		t.Fatal("the handle that is not silenced wrote nothing for the same statements")
	}
	assertNoneCarries(t, records, silencedValue)
}

// TestLogModeReturnsACopy checks that LogMode returns a logger at the given level and leaves the
// original and its settings alone.
func TestLogModeReturnsACopy(t *testing.T) {
	ctx := t.Context()
	rec := &recordingHandler{}
	original := newStatementLogger(slog.New(rec))
	original.slow = emissionThreshold

	changed := original.LogMode(logger.Info)
	if original.level != logger.Warn {
		t.Errorf("LogMode changed the logger it was called on to level %d, want it left at Warn", original.level)
	}
	original.Info(ctx, "information")
	if records := rec.written(); len(records) != 0 {
		t.Fatalf("the original logger wrote an informational message at level Warn: %v", everythingOf(records))
	}
	changed.Info(ctx, "information")
	if got := messagesOf(rec.written()); !slices.Equal(got, []string{"gorm info"}) {
		t.Fatalf("the copy at Info wrote %v, want one informational message", got)
	}

	rec.reset()
	changed.Trace(ctx, began(emissionTook), rec.statement("SELECT slow", 1), nil)
	records := rec.written()
	if len(records) != 1 {
		t.Fatalf("the copy wrote %d records for a slow statement, want one:\n%s", len(records), everythingOf(records))
	}
	if threshold, _ := durationOf(attributesOf(t, records[0]), "threshold"); threshold != emissionThreshold {
		t.Errorf("the copy runs at the threshold %v, want the original's %v carried over", threshold, emissionThreshold)
	}

	silent := changed.LogMode(logger.Silent)
	rec.reset()
	changed.Error(ctx, "an error")
	silent.Error(ctx, "an error")
	if got := messagesOf(rec.written()); !slices.Equal(got, []string{"gorm error"}) {
		t.Errorf("the copy and its own copy at Silent wrote %v, want the first alone: one copy's level is not "+
			"the other's", got)
	}
}

// TestTheRecordNamesTheCallSiteNotTheLogger checks that every record names the line that made the
// call, by both its program counter and its caller attribute.
func TestTheRecordNamesTheCallSiteNotTheLogger(t *testing.T) {
	t.Run("a call the case makes itself", func(t *testing.T) {
		ctx := t.Context()
		rec := &recordingHandler{}
		l := directLogger(rec, logger.Info, emissionThreshold)
		var lines []int

		lines = append(lines, nextLine())
		l.Info(ctx, "information")
		lines = append(lines, nextLine())
		l.Warn(ctx, "a warning")
		lines = append(lines, nextLine())
		l.Error(ctx, "an error")
		lines = append(lines, nextLine())
		l.Trace(ctx, began(emissionTook), rec.statement("SELECT 1", 1), nil)
		lines = append(lines, nextLine())
		l.Trace(ctx, began(briefly), rec.statement("SELECT 1", 1), errors.New("refused"))
		lines = append(lines, nextLine())
		l.Trace(ctx, began(briefly), rec.statement("SELECT 1", 1), nil)

		records := rec.written()
		wantMessages := []string{
			"gorm info", "gorm warning", "gorm error", "slow statement", "statement failed", "statement",
		}
		if got := messagesOf(records); !slices.Equal(got, wantMessages) {
			t.Fatalf("the calls were written as %v, want %v", got, wantMessages)
		}
		for i, record := range records {
			attrs := expectRecord(t, record, wantMessages[i], lines[i])
			if frame := siteOf(record); frame.Line != lines[i] {
				t.Errorf("the %q record's program counter resolves to line %d, want the line %d of the call that "+
					"caused it (%s)", record.Message, frame.Line, lines[i], attrs["caller"])
			}
		}
	})

	t.Run("a statement GORM runs", func(t *testing.T) {
		rec := &recordingHandler{}
		handle := loggingOn(t, rec, notSlow).Debug()
		var (
			lines []int
			id    uint
			found []loggedEntry
		)

		lines = append(lines, nextLine())
		_ = handle.Create(&loggedEntry{ID: 1, Value: "site-1"}).Error
		lines = append(lines, nextLine())
		_ = handle.Where("value = ?", "site-1").Find(&found).Error
		lines = append(lines, nextLine())
		_ = handle.Where("value = ?", "site-2").First(&loggedEntry{}).Error
		lines = append(lines, nextLine())
		_ = handle.Exec("UPDATE logged_entries SET value = value WHERE id = ?", 1).Error
		lines = append(lines, nextLine())
		_ = handle.Raw("SELECT id FROM logged_entries WHERE value = ?", "site-1").Scan(&id).Error
		lines = append(lines, nextLine())
		_ = handle.Raw("SELECT id FROM logged_entries WHERE value = ?", "site-1").Row().Scan(&id)

		records := rec.written()
		wantMessages := []string{"statement", "statement", "statement failed", "statement", "statement", "statement"}
		if got := messagesOf(records); !slices.Equal(got, wantMessages) {
			t.Fatalf("the statements were written as %v, want %v", got, wantMessages)
		}
		for i, record := range records {
			expectRecord(t, record, wantMessages[i], lines[i])
			if frame := siteOf(record); frame.Line != lines[i] {
				t.Errorf("the %q record's program counter resolves to line %d, want the line %d of the call that "+
					"caused it", record.Message, frame.Line, lines[i])
			}
		}
	})
}

// TestARecordWithNoCallSiteCarriesNeitherMark checks that a record with no call site carries
// neither a program counter nor a caller attribute.
func TestARecordWithNoCallSiteCarriesNeitherMark(t *testing.T) {
	rec := &recordingHandler{}
	statements := newStatementLogger(slog.New(rec))
	statements.write(t.Context(), slog.LevelInfo, "gorm info", runtime.Frame{}, slog.String("detail", "a message"))

	records := rec.written()
	if len(records) != 1 {
		t.Fatalf("%d records were written, want one", len(records))
	}
	if records[0].PC != 0 {
		t.Errorf("the record carries the program counter %#x, want none", records[0].PC)
	}
	attrs := attributesOf(t, records[0])
	if caller, carried := attrs["caller"]; carried {
		t.Errorf("the record carries the caller %q, want none", caller)
	}
	if detail, _ := textOf(attrs, "detail"); detail != "a message" {
		t.Errorf("the record carries the detail %q, want the one it was given", detail)
	}
}

// TestARecordTheHeldLoggerWillNotWriteIsNeverRendered checks that a record the held logger would
// not write is never rendered and never handed over.
func TestARecordTheHeldLoggerWillNotWriteIsNeverRendered(t *testing.T) {
	cases := []struct {
		name string
		from slog.Level
	}{
		{"every level", slog.LevelDebug},
		{"Info and above", slog.LevelInfo},
		{"Warn and above", slog.LevelWarn},
		{"Error and above", slog.LevelError},
		{"no level", slog.LevelError + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			rec.allowFrom(c.from)
			l := directLogger(rec, logger.Info, emissionThreshold)

			var wantTrail, wantMessages []string
			for _, e := range emissions {
				e.issue(t.Context(), l, rec)
				wantTrail = append(wantTrail, "enabled "+e.level.String())
				if e.level >= c.from {
					wantTrail = append(wantTrail, "render", "handle")
					wantMessages = append(wantMessages, e.message)
				}
			}
			if got := rec.events(); !slices.Equal(got, wantTrail) {
				t.Errorf("the handler was asked and handed %v, want %v: a record the held logger will not write is "+
					"neither rendered nor handed over, and one it will write is rendered after the question and "+
					"before the delivery", got, wantTrail)
			}
			if got := messagesOf(rec.written()); !slices.Equal(got, wantMessages) {
				t.Errorf("the records written are %v, want %v", got, wantMessages)
			}
		})
	}
}

type contextKey struct{}

// TestANilContextIsTolerated checks that a nil context is replaced before it reaches the handler
// and a non-nil one is handed on as it is.
func TestANilContextIsTolerated(t *testing.T) {
	run := func(t *testing.T, ctx context.Context) *recordingHandler {
		t.Helper()
		rec := &recordingHandler{}
		l := directLogger(rec, logger.Info, emissionThreshold)
		for _, e := range emissions {
			e.issue(ctx, l, rec)
		}
		if got := len(rec.written()); got != len(emissions) {
			t.Fatalf("%d of the %d records were written", got, len(emissions))
		}
		return rec
	}

	t.Run("nil", func(t *testing.T) {
		var missing context.Context
		rec := run(t, missing)
		for i, ctx := range rec.asked {
			if ctx == nil {
				t.Errorf("the handler was asked about question %d with a nil context", i+1)
			}
		}
		for i, ctx := range rec.handled {
			if ctx == nil {
				t.Errorf("the handler was handed record %d with a nil context", i+1)
			}
		}
	})

	t.Run("a context with a value", func(t *testing.T) {
		rec := run(t, context.WithValue(t.Context(), contextKey{}, "carried"))
		for i, ctx := range rec.asked {
			if ctx.Value(contextKey{}) != "carried" {
				t.Errorf("the handler was asked about question %d with a context that lost its value", i+1)
			}
		}
		for i, ctx := range rec.handled {
			if ctx.Value(contextKey{}) != "carried" {
				t.Errorf("the handler was handed record %d with a context that lost its value", i+1)
			}
		}
	})
}

// TestAStatementLoggerOverNoLoggerPanics checks that the constructor refuses a nil logger with a
// message.
func TestAStatementLoggerOverNoLoggerPanics(t *testing.T) {
	defer func() {
		reported := recover()
		if reported == nil {
			t.Fatal("newStatementLogger accepted a nil logger")
		}
		text, ok := reported.(string)
		if !ok {
			t.Fatalf("newStatementLogger panicked with %T, want a message", reported)
		}
		if text == "" {
			t.Error("newStatementLogger panicked with an empty message")
		}
	}()
	newStatementLogger(nil)
}

type refusingHandler struct{}

func (refusingHandler) Enabled(context.Context, slog.Level) bool { return false }

func (refusingHandler) Handle(context.Context, slog.Record) error {
	panic("refusingHandler: the logger handed a record to a handler that refused its level")
}

func (h refusingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h refusingHandler) WithGroup(string) slog.Handler      { return h }

// TestARecordTheHeldLoggerWillNotWriteCostsNoAllocation checks that a record the held logger would
// not write allocates nothing after the question is asked.
func TestARecordTheHeldLoggerWillNotWriteCostsNoAllocation(t *testing.T) {
	var (
		ctx     = context.Background()
		refused = errors.New("refused")
		data    = []any{"data"}
		fc      = func() (string, int64) { return "SELECT 1", 1 }
		quick   = time.Now()
		slowed  = began(emissionTook)
		held    = slog.New(refusingHandler{})
	)
	unremarkable := newStatementLogger(held)
	unremarkable.slow = notSlow
	unremarkable.level = logger.Info
	slow := newStatementLogger(held)
	slow.level = logger.Info

	cases := []struct {
		name string
		call func()
	}{
		{"a statement that failed", func() { unremarkable.Trace(ctx, quick, fc, refused) }},
		{"a statement slower than the threshold", func() { slow.Trace(ctx, slowed, fc, nil) }},
		{"a statement that ran as any other", func() { unremarkable.Trace(ctx, quick, fc, nil) }},
		{"an informational message", func() { unremarkable.Info(ctx, "message %v", data...) }},
		{"a warning", func() { unremarkable.Warn(ctx, "message %v", data...) }},
		{"an error message", func() { unremarkable.Error(ctx, "message %v", data...) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if allocations := testing.AllocsPerRun(100, c.call); allocations != 0 {
				t.Errorf("%s that the held logger would not write cost %v allocations per call, want none: "+
					"the call site and the statement are read only for a record that is written", c.name, allocations)
			}
		})
	}
}

func attributeOrder(record slog.Record) []string {
	var keys []string
	record.Attrs(func(attr slog.Attr) bool {
		keys = append(keys, attr.Key)
		return true
	})
	return keys
}

// TestEveryKindOfRecordCarriesItsAttributesInOrder checks the order of each record's attributes:
// distinguishing ones, then elapsed, rows and statement, then the call site.
func TestEveryKindOfRecordCarriesItsAttributesInOrder(t *testing.T) {
	refused := fmt.Errorf("%w: %w", gorm.ErrDuplicatedKey, quotingError{failedValue, "23505"})
	cases := []struct {
		name    string
		message string
		issue   func(ctx context.Context, l logger.Interface, rec *recordingHandler)
		order   []string
	}{
		{
			"a failed statement whose error carries classes", "statement failed",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(briefly), rec.statement("SELECT 1", 7), refused)
			},
			[]string{"classes", "elapsed", "rows", "statement", "caller"},
		},
		{
			"a failed statement whose error carries none", "statement failed",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(briefly), rec.statement("SELECT 1", 7), errors.New("refused"))
			},
			[]string{"elapsed", "rows", "statement", "caller"},
		},
		{
			"a failed statement with no statement built and no rows counted", "statement failed",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(briefly), rec.statement("", -1), errors.New("refused"))
			},
			[]string{"elapsed", "caller"},
		},
		{
			"a slow statement", "slow statement",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(emissionTook), rec.statement("SELECT 1", 7), nil)
			},
			[]string{"threshold", "elapsed", "rows", "statement", "caller"},
		},
		{
			"a statement that ran as any other", "statement",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Trace(ctx, began(briefly), rec.statement("SELECT 1", 7), nil)
			},
			[]string{"elapsed", "rows", "statement", "caller"},
		},
		{
			"an informational message of GORM's", "gorm info",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Info(ctx, "a message about %v", rec.probe())
			},
			[]string{"detail", "caller"},
		},
		{
			"a warning of GORM's", "gorm warning",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Warn(ctx, "a message about %v", rec.probe())
			},
			[]string{"detail", "caller"},
		},
		{
			"an error message of GORM's", "gorm error",
			func(ctx context.Context, l logger.Interface, rec *recordingHandler) {
				l.Error(ctx, "a message about %v", rec.probe())
			},
			[]string{"detail", "caller"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			c.issue(t.Context(), directLogger(rec, logger.Info, emissionThreshold), rec)

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("%d records were written, want one", len(records))
			}
			expectRecord(t, records[0], c.message, 0)
			if got := attributeOrder(records[0]); !slices.Equal(got, c.order) {
				t.Errorf("the record carries its attributes in the order %v, want %v", got, c.order)
			}
		})
	}
}

// TestTheClassesOfAnErrorAreWrittenInAFixedOrder checks that all three kinds of class are joined in
// the documented order whatever order the chain lists them.
func TestTheClassesOfAnErrorAreWrittenInAFixedOrder(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		classes string
	}{
		{
			"a sentinel, a context's end and a SQLSTATE",
			fmt.Errorf("%w: %w: %w", context.DeadlineExceeded, gorm.ErrDuplicatedKey, quotingError{failedValue, "23505"}),
			"duplicated key not allowed, context deadline exceeded, SQLSTATE 23505",
		},
		{
			"a sentinel, a context's end and a result code",
			fmt.Errorf("%w: %w: %w", context.Canceled, gorm.ErrForeignKeyViolated, resultCodeError{failedValue, 787}),
			"violates foreign key constraint, context canceled, result code 787",
		},
		{
			"every class an error can carry",
			fmt.Errorf("%w: %w: %w: %w: %w: %w: %w", resultCodeError{failedValue, 2067}, quotingError{failedValue, "23505"},
				context.DeadlineExceeded, context.Canceled, gorm.ErrForeignKeyViolated, gorm.ErrDuplicatedKey,
				gorm.ErrRecordNotFound),
			"record not found, duplicated key not allowed, violates foreign key constraint, context canceled, " +
				"context deadline exceeded, SQLSTATE 23505, result code 2067",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			directLogger(rec, logger.Warn, notSlow).Trace(t.Context(), began(briefly), rec.statement("SELECT ?", -1), c.err)

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("the failure was written as %d records, want one", len(records))
			}
			attrs := expectRecord(t, records[0], "statement failed", 0)
			if classes, _ := textOf(attrs, "classes"); classes != c.classes {
				t.Errorf("the failure is written with the classes %q, want %q", classes, c.classes)
			}
		})
	}
}

type failingHandler struct {
	*recordingHandler
	err error
}

func (h failingHandler) Handle(ctx context.Context, record slog.Record) error {
	_ = h.recordingHandler.Handle(ctx, record)
	return h.err
}

// TestAStatementReportsTheSameToItsCallerWhetherOrNotItsRecordWasWritten checks that a handler's
// failure to write does not change what the statement reports.
func TestAStatementReportsTheSameToItsCallerWhetherOrNotItsRecordWasWritten(t *testing.T) {
	const (
		failing = "INSERT INTO logged_missing (value) VALUES (?)"
		passing = "INSERT INTO logged_entries (value) VALUES (?)"
	)
	cases := []struct {
		name      string
		statement string
		debug     bool
		fails     bool
		message   string
	}{
		{"a statement that failed", failing, false, true, "statement failed"},
		{"a statement that failed in a Debug session", failing, true, true, "statement failed"},
		{"a statement of a Debug session", passing, true, false, "statement"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handlerErr := errors.New("the handler could not write the record")
			rec := &recordingHandler{}
			statements := newStatementLogger(slog.New(failingHandler{rec, handlerErr}))
			statements.slow = notSlow
			base := dbsuite.OpenSQLite(t)
			if err := base.Exec("CREATE TABLE logged_entries (id INTEGER PRIMARY KEY, value TEXT NOT NULL UNIQUE)").Error; err != nil {
				t.Fatalf("creating logged_entries: %v", err)
			}
			session := func(l logger.Interface) *gorm.DB {
				handle := base.Session(&gorm.Session{Logger: l})
				if c.debug {
					handle = handle.Debug()
				}
				return handle.WithContext(t.Context())
			}

			var (
				got      error
				panicked any
			)
			func() {
				defer func() { panicked = recover() }()
				got = session(statements).Exec(c.statement, filterValue).Error
			}()
			if panicked != nil {
				t.Fatalf("the statement panicked because its record could not be written: %v", panicked)
			}
			want := session(logger.Discard).Exec(c.statement, sessionValue).Error
			if (got != nil) != c.fails || (want != nil) != c.fails {
				t.Fatalf("the statement returned %v with the failing handler and %v without a logger, want failure: %t",
					got, want, c.fails)
			}
			if c.fails {
				if errors.Is(got, handlerErr) {
					t.Errorf("the statement's error carries the handler's: %v", got)
				}
				if got.Error() != want.Error() {
					t.Errorf("the statement returned %q, want its own error, %q", got, want)
				}
			}
			if kept := messagesOf(rec.written()); !slices.Equal(kept, []string{c.message}) {
				t.Errorf("the handler was handed %v, want the one record %q", kept, c.message)
			}
			if handled := slices.DeleteFunc(rec.events(), func(event string) bool { return event != "handle" }); len(handled) != 1 {
				t.Errorf("Handle was called %d times, want once", len(handled))
			}
		})
	}
}

// TestAStatementIsWrittenAsGORMRenderedIt checks that the statement attribute is GORM's text whole,
// over several lines and uncut.
func TestAStatementIsWrittenAsGORMRenderedIt(t *testing.T) {
	text := "\n\t-- a migration file begins with its comments\nCREATE TABLE notes (\n  id INTEGER PRIMARY KEY,\n" +
		"  body TEXT NOT NULL -- " + strings.Repeat("a long line of a migration ", 12) + "\n)  \n"
	cases := []struct {
		name    string
		level   logger.LogLevel
		took    time.Duration
		err     error
		message string
	}{
		{"a failed statement", logger.Warn, briefly, errors.New("refused"), "statement failed"},
		{"a slow statement", logger.Warn, emissionTook, nil, "slow statement"},
		{"a statement that ran as any other", logger.Info, briefly, nil, "statement"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingHandler{}
			directLogger(rec, c.level, emissionThreshold).Trace(t.Context(), began(c.took), rec.statement(text, 1), c.err)

			records := rec.written()
			if len(records) != 1 {
				t.Fatalf("%d records were written, want one", len(records))
			}
			attrs := expectRecord(t, records[0], c.message, 0)
			if got, _ := textOf(attrs, "statement"); got != text {
				t.Errorf("the statement attribute is %q, want the text GORM rendered, %q", got, text)
			}
		})
	}
}

type panickingHandler struct{ *recordingHandler }

func (panickingHandler) Handle(context.Context, slog.Record) error { panic("the handler cannot cope") }

// TestAPanicInTheHandlerIsNotCaught checks that a handler's panic reaches the caller of the
// statement.
func TestAPanicInTheHandlerIsNotCaught(t *testing.T) {
	statements := newStatementLogger(slog.New(panickingHandler{&recordingHandler{}}))
	statements.slow = notSlow
	handle := dbsuite.OpenSQLite(t).Session(&gorm.Session{Logger: statements})

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		_ = handle.Exec("INSERT INTO logged_missing (value) VALUES (?)", filterValue).Error
	}()
	if panicked != "the handler cannot cope" {
		t.Errorf("the statement's call ended with the panic %v, want the handler's own", panicked)
	}
}
