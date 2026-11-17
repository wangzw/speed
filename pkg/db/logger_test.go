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

// The values the cases below bind, each one a mark the records are searched
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
	finisherValue   = "logged-value-finisher"
	filterValue     = "logged-value-filter-probe"
	sessionValue    = "logged-value-session"
	silencedValue   = "logged-value-silenced"
)

// withheldText is what a record carries in place of the text of a statement
// GORM's Scan finisher has filled its values into. It is written out here, not
// read from the logger, so that rewording it is a change these cases see.
const withheldText = "statement withheld: GORM filled its values in before the logger saw it"

// notSlow is a threshold no statement a case runs reaches, for the cases that
// need a statement written as what it is and not as a slow one.
const notSlow = time.Hour

// briefly is how long the cases that call Trace themselves say a statement that
// ran as any other took: a positive time whatever the resolution of the clock,
// which can report no time at all between two readings taken one after the
// other, and short of every threshold a case sets.
const briefly = time.Millisecond

// began is when a statement that has taken the given time began.
func began(took time.Duration) time.Time { return time.Now().Add(-took) }

// thisFile is this file's path as the runtime reports it in a frame. Every call
// the cases below make that reaches the logger is made from this file, so it is
// the call site every record has to name.
var thisFile = func() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}()

// nextLine is the line after the one it is called from: where the call a case
// is about to make stands, when the case writes that call on the next line. It
// is the expected call site of the record the call causes, taken from the
// source and not from the logger.
func nextLine() int {
	_, _, line, _ := runtime.Caller(1)
	return line + 1
}

// recordingHandler is the slog.Handler every case here writes through.
//
// It keeps a copy of each record it is handed, answers Enabled from a
// predicate a case sets, and keeps a trail of what it was asked, in order: the
// level of each Enabled, each rendering a case watches for (see statement and
// probe), and each Handle.
//
// Handle does not judge the level. The handler behind the log module's level
// layer does not either, which is why the logger has to ask Enabled before it
// writes; a handler that gated again here would pass a logger that never asked.
type recordingHandler struct {
	mu sync.Mutex
	// allows is the predicate Enabled answers from. Nil allows every level.
	allows func(slog.Level) bool
	// kept are the copies of the records Handle was handed.
	kept []slog.Record
	// trail is what the handler was asked and what a case rendered, in order.
	trail []string
	// asked and handled are the contexts Enabled and Handle were handed.
	asked, handled []context.Context
}

var _ slog.Handler = (*recordingHandler)(nil)

// Enabled notes the question and answers it from the predicate.
func (h *recordingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trail = append(h.trail, "enabled "+level.String())
	h.asked = append(h.asked, ctx)
	return h.allows == nil || h.allows(level)
}

// Handle keeps a copy of the record, whatever its level.
func (h *recordingHandler) Handle(ctx context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trail = append(h.trail, "handle")
	h.handled = append(h.handled, ctx)
	h.kept = append(h.kept, record.Clone())
	return nil
}

// WithAttrs refuses: no case binds attributes to the logger it writes through,
// and a handler that dropped them would hide their absence from the record.
func (*recordingHandler) WithAttrs([]slog.Attr) slog.Handler {
	panic("recordingHandler: no case binds attributes to the logger")
}

// WithGroup refuses for the reason WithAttrs gives.
func (*recordingHandler) WithGroup(string) slog.Handler {
	panic("recordingHandler: no case opens a group on the logger")
}

// allowFrom makes Enabled answer true from the given level up, as the log
// module's level layer does.
func (h *recordingHandler) allowFrom(level slog.Level) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allows = func(asked slog.Level) bool { return asked >= level }
}

// written is a copy of the records the handler kept.
func (h *recordingHandler) written() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.kept)
}

// events is a copy of the trail.
func (h *recordingHandler) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.trail)
}

// reset forgets the records, the trail and the contexts, and leaves the
// predicate as it is.
func (h *recordingHandler) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept, h.trail, h.asked, h.handled = nil, nil, nil, nil
}

// note adds an event to the trail.
func (h *recordingHandler) note(event string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trail = append(h.trail, event)
}

// statement is the function a Trace call is handed to render its statement. It
// notes the rendering, so that a case can tell a statement that was rendered
// from one that was only asked about.
func (h *recordingHandler) statement(text string, rows int64) func() (string, int64) {
	return func() (string, int64) {
		h.note("render")
		return text, rows
	}
}

// probe is a value a message is formatted with. Formatting it is the rendering
// of a message, which the trail notes as it notes a statement's.
func (h *recordingHandler) probe() fmt.Stringer { return renderProbe{h} }

// renderProbe is what probe hands out.
type renderProbe struct{ rec *recordingHandler }

func (p renderProbe) String() string {
	p.rec.note("render")
	return "probed"
}

// vocabulary is what the logger says, by message: the level a record is written
// at, the attributes it always carries, and those it carries only when it has
// something to put in them. It is written out here and read from nowhere in the
// logger, so that rewording a message, moving one to another level or renaming
// an attribute is a change these cases see: the messages and the keys are what
// an operator searches a destination for and what a handler's rules judge.
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

// attributeKinds is the kind of value each attribute carries.
var attributeKinds = map[string]slog.Kind{
	"classes":   slog.KindString,
	"threshold": slog.KindDuration,
	"elapsed":   slog.KindDuration,
	"rows":      slog.KindInt64,
	"statement": slog.KindString,
	"caller":    slog.KindString,
	"detail":    slog.KindString,
}

// withoutSlowStatements is records with the "slow statement" records left out.
//
// A statement slower than the logger's threshold is written as "slow
// statement" at Warn whatever else it did, and the threshold is a fixed couple
// of hundred milliseconds: on a loaded machine an ordinary statement — a
// CREATE TABLE, an INSERT — crosses it. A case that counts the records of
// statements it expects to be unwritten, or to be exactly one failure, is not
// about that record; a count that included it would pass or fail with the
// machine's load. The slow statement's own emission has its cases, so this
// helper only narrows a count. No assertion about what a record carries reads
// its result: a slow statement's redaction is still asserted over every
// record.
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

// attributesOf is what a record carries, by key. A key carried twice, or a
// group, fails the case: the logger writes each attribute once, and flat.
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

// everything is all a record carries, as text: its message, and the key and
// value of each attribute. What a case looks for is absent from the record when
// it is absent from this.
func everything(record slog.Record) string {
	var text strings.Builder
	text.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		text.WriteString(" " + attr.Key + "=" + attr.Value.String())
		return true
	})
	return text.String()
}

// textOf is the string attribute a record carries under key, and whether it
// carries one.
func textOf(attrs map[string]slog.Value, key string) (string, bool) {
	value, carried := attrs[key]
	if !carried || value.Kind() != slog.KindString {
		return "", false
	}
	return value.String(), true
}

// durationOf is the duration attribute a record carries under key, and whether
// it carries one.
func durationOf(attrs map[string]slog.Value, key string) (time.Duration, bool) {
	value, carried := attrs[key]
	if !carried || value.Kind() != slog.KindDuration {
		return 0, false
	}
	return value.Duration(), true
}

// countOf is the integer attribute a record carries under key, and whether it
// carries one.
func countOf(attrs map[string]slog.Value, key string) (int64, bool) {
	value, carried := attrs[key]
	if !carried || value.Kind() != slog.KindInt64 {
		return 0, false
	}
	return value.Int64(), true
}

// siteOf is where a record's program counter resolves to, the way a handler that
// adds the source to its output resolves it.
func siteOf(record slog.Record) runtime.Frame {
	if record.PC == 0 {
		return runtime.Frame{}
	}
	frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
	return frame
}

// checkVocabulary holds a record to everything every record the logger writes
// has in common, and returns what it carries by key: it says one of the
// logger's own words, at that word's level, with a time; it carries the
// attributes of its kind and none besides, each of the kind of value it is
// meant to be; its elapsed time is a positive duration; it names no empty
// statement; and its call site is this file, by its program counter and by its
// caller attribute alike, and never the logger.
//
// Every case that reads records calls it on each one, so the vocabulary is held
// on every path a statement or a message takes to the logger, not on one.
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

// checkCallSite holds the call site a record names to this file, by its program
// counter, resolved through runtime.CallersFrames, and by its caller attribute,
// which has to be the same file and line: the logger is neither of them.
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

// expectRecord holds a record to the vocabulary and to the message the case
// expects of it. A line other than 0 is the line of the call that caused it,
// which the record has to name as its call site.
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

// assertNoneCarries fails the case when any of the records carries one of the
// values, in its message or in any attribute.
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

// loggedEntry is a row of logged_entries.
type loggedEntry struct {
	ID    uint
	Value string
}

func (loggedEntry) TableName() string { return "logged_entries" }

// loggingOn is a handle whose statements log through a statementLogger writing
// to rec with the threshold slow, on a database holding the table the cases
// write.
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

// directLogger is a statement logger writing through rec at the GORM level,
// with the slow threshold, for the cases that call its methods themselves.
func directLogger(rec *recordingHandler, level logger.LogLevel, slow time.Duration) logger.Interface {
	statements := newStatementLogger(slog.New(rec))
	statements.slow = slow
	return statements.LogMode(level)
}

// emission is one way GORM reaches the logger, with the record the logger writes
// for it. The six of them are every message the logger has, and between them
// the four methods GORM calls.
type emission struct {
	// name is what a failure calls it.
	name string
	// message and level are the record the logger writes for it.
	message string
	level   slog.Level
	// needle is a word its statement or its message carries, and carrier is the
	// attribute that holds it.
	needle, carrier string
	// issue sends it to the logger. The statements it sends are rendered through
	// rec, and the messages are formatted with one of its probes.
	issue func(ctx context.Context, l logger.Interface, rec *recordingHandler)
}

const (
	// emissionThreshold is the slow threshold the cases that run the emissions
	// set, and emissionTook how long the slow statement is said to have taken: a
	// statement begun at once has taken no time to speak of, and the failed and
	// the unremarkable statements are begun at once.
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

// TestEveryKindOfRecordCarriesExactlyItsAttributes holds the vocabulary the
// logger writes in: each message, the level its record is written at, the
// attributes the record carries as a set and the kind of value each one holds.
// A key added, left out or renamed fails here, and so does a message reworded or
// moved to another level, a duration written as a number or a count as text.
//
// The statement and GORM's own text are attributes and not the message because
// the redaction layer judges attributes, and only attributes: a rule registered
// for a key reaches what is in an attribute under it, and reaches nothing that
// was written into a message.
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

// TestTheLoggerWritesNoValueOnAnyPath holds the three ways a statement reaches
// the logger — slower than the threshold, failed, and every statement of a
// Debug session — to writing the statement's text and none of its values, and
// the lookup that found no row to the same. The statements it issues are the
// ones GORM renders for the logger; the ones the Scan finisher runs are held by
// TestAStatementRunThroughScanIsWrittenWithoutItsText.
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

// everythingOf is the text of each of the records, one to a line.
func everythingOf(records []slog.Record) string {
	lines := make([]string, len(records))
	for i, record := range records {
		lines[i] = everything(record)
	}
	return strings.Join(lines, "\n")
}

// TestAStatementRunThroughScanIsWrittenWithoutItsText holds the logger to the
// one path on which GORM never asks it for the values a statement binds: the
// Scan finisher swaps the handle's logger for a recorder, whose ParamsFilter
// hands every value back, runs the statement on the recorder, and gives the
// handle's logger the finished text, values in, through Trace alone. The
// logger's own ParamsFilter is not asked there, so what the logger can do is
// withhold the text. Whichever way the statement reaches the logger — failed,
// slower than the threshold, in a Debug session — and however it is built, its
// record carries no value and has the withheld text where the statement would
// be, and it carries what every record carries: the classes of the error,
// the time, the rows and the call site.
func TestAStatementRunThroughScanIsWrittenWithoutItsText(t *testing.T) {
	cases := []struct {
		name  string
		value string
		slow  time.Duration
		debug bool
		// issue runs the statement on a handle that holds a row under value and
		// returns the error it reports, which fails says whether to expect.
		issue func(handle *gorm.DB, value string) error
		fails bool
		// message is the record the statement is written as, classes the classes
		// it carries, threshold the one it exceeded, and rows what it counts,
		// when rowsKnown says the case holds it to a count.
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

// TestAStatementRunThroughAnotherFinisherKeepsItsTextWhereScanWithholdsIt is
// the control of the case above. Row, Rows and the generic interface's Scan,
// unlike the Scan finisher, leave the handle's logger in place, so GORM asks the
// logger's ParamsFilter for the values and in a Debug session the record names
// the statement with its placeholder and carries no value. The text is withheld
// only where it has to be. GORM counts no rows for a Row or a Rows, which is the
// -1 the record leaves out.
func TestAStatementRunThroughAnotherFinisherKeepsItsTextWhereScanWithholdsIt(t *testing.T) {
	const statement = "SELECT id FROM logged_entries WHERE value = ?"
	cases := []struct {
		name string
		run  func(t *testing.T, handle *gorm.DB, value string)
		// uncounted says the statement is one GORM counts no rows for.
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

// TestAScanThatRanNoStatementIsNotWrittenAsWithheld holds the withheld text to
// the statements that exist. A Scan on a handle that already carries an error
// runs nothing, and on a chain that has not built its statement yet — a Model
// with a Where, no Raw — there is none to hand over: GORM gives the
// logger the handle's error through Trace, with the empty text of a recorder
// that saw no statement. That record has nothing to withhold, carries no
// statement, and does not say it withheld one.
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

// TestAFailedStatementIsWrittenWithTheClassesOfItsError covers the error half:
// the text of an error can quote what the statement bound or read, so the
// logger writes the classes the error carries — the engine's code, GORM's
// sentinels, a context's end — and never the text.
//
// The spelling is what the cases hold exactly, because it is what an operator
// searches for and what the host's own checks match on: the text SQLSTATE
// followed by the code, and result code followed by the number, with the
// classes of one error joined by a comma and a space. An error that carries none
// gives no classes attribute at all.
func TestAFailedStatementIsWrittenWithTheClassesOfItsError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		// classes is the attribute's text, and empty where the record has none.
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

// TestTheStatementAndGORMsTextAreAttributesNeverTheMessage holds the text a
// record is about to the attribute that carries it. The statement of a
// statement record and the formatted text of one of GORM's own are in the
// statement and detail attributes and nowhere else: the message is one of the
// logger's own words, whatever is written. A message that carried the text
// would take what the statement says past the redaction layer, which judges
// attributes and not messages.
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

// TestElapsedIsTheTimeTheStatementTook holds the elapsed attribute to the time
// since the statement began, as a duration, on each way a statement can be
// written. A statement begun a while ago has taken at least that long, and a
// duration of nothing is not a time anything took.
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

// TestASlowStatementCarriesTheThresholdItExceeded holds the threshold attribute
// of a slow statement to the threshold the logger runs at, a statement that
// stays under it to being no slow statement, a statement that failed past it to
// being written as a failure and not as a slow statement, and the threshold a
// logger is built with to the 200ms of GORM's default logger, which is the
// bound a slow statement is written past.
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

// TestRowsAreLeftOutWhenGORMCountedNone holds the rows attribute to the count
// GORM reports, and to being absent when GORM counted none, which it reports as
// -1. A count of nothing is a count: 0 is written.
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

// TestGORMsOwnMessagesGoToTheDetailAttribute holds what GORM says on its own —
// a callback replaced, a model the migrator cannot parse — to being written as
// GORM formats it: the message formatted with its data, without the newline
// several of GORM's messages end in, in the detail attribute, under a message
// that is the same whatever GORM says.
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

// TestGORMsOwnMessagesReachTheLoggerFromInsideGORM runs the three of GORM's own
// messages a callback table produces through GORM itself, on a handle whose
// root logger is the statement logger at Info: a callback replaced is an
// informational message, one removed is a warning, and a registration the table
// cannot order is an error. Each is a record with GORM's text formatted in and
// the call site outside GORM, which here is the line that made the call, and
// that is the same call site GORM's own text names in the first two.
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

// TestADebugSessionIsWrittenAsInfoRecords holds the level of the statements a
// Debug session asks for. GORM's Info is slog's Info, so a statement that ran as
// any other is an Info record, and a held logger that writes from Info up, as
// the log module's default level does, writes every one of them; a held logger
// that writes from Warn up writes none of them, and writes the statement
// that failed.
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

// messagesOf is the message of each of the records, in order.
func messagesOf(records []slog.Record) []string {
	messages := make([]string, len(records))
	for i, record := range records {
		messages[i] = record.Message
	}
	return messages
}

// trailOf is the trail a handler keeps when the logger asks it about each of the
// wanted records, finds each one written, renders it and hands it over: the
// level asked, the rendering and the delivery, in that order. Each wanted record
// is a level and its message, as "WARN slow statement".
func trailOf(wanted []string) []string {
	var trail []string
	for _, record := range wanted {
		level, _, _ := strings.Cut(record, " ")
		trail = append(trail, "enabled "+level, "render", "handle")
	}
	return trail
}

// TestTheLoggerWritesByItsLevel holds the logger to GORM's levels: at the
// default one it writes failed and slow statements and GORM's warnings and
// errors, not its informational messages; at Error only failures; at Silent
// nothing; at Info everything, a statement that ran as any other included.
//
// GORM's own level is judged first. A call it does not let through never asks
// the held logger anything, so the trail of the handler is the records that were
// written and nothing besides. A slow statement is written as one at Info as at
// Warn, and not as the unremarkable statement an Info level would otherwise ask
// for.
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

// TestASilencedHandleWritesNothing holds Silent to writing nothing from a real
// handle, on the paths a statement takes to the logger: a statement that ran, a
// statement that failed, a lookup that found no row, one run through Scan and one
// through Row, on a handle whose threshold makes every statement slow. The
// handler is not even asked. The same statements on the handle that is not
// silenced are written, so a silence that came from the statements never
// reaching the logger is not mistaken for the logger's.
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

// TestLogModeReturnsACopy holds LogMode to returning a logger at the level it is
// given and leaving the one it was called on alone, with what the copy runs on
// carried over: the logger it writes through and the slow threshold. GORM calls
// it on every Debug and every Session, so a LogMode that changed the logger it
// was called on would turn the handle's own logger up for every statement after.
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

// TestTheRecordNamesTheCallSiteNotTheLogger holds the call site of every record
// to the line that made the call, by the record's program counter, which a
// handler that adds the source resolves, and by its caller attribute, which the
// log module's chain depends on because it never adds the source. The call site
// is the first frame outside GORM: for GORM's own messages and statements the
// line of the call into GORM, for a call the case makes itself the line that
// makes it, and never the logger's own file, which every one of them passes
// through.
//
// The lines are the ones the cases are written on, taken from the source with
// nextLine and not from the logger. Each call stands on the line after the one
// that records it.
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

// TestARecordWithNoCallSiteCarriesNeitherMark holds the two marks of a call site
// together where there is none to name. GORM's lookup reports no frame when it
// finds none outside GORM, and a record built from that carries no program
// counter and no caller attribute; a program counter without a caller, or a
// caller without a program counter, would be one mark contradicting the other.
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

// TestARecordTheHeldLoggerWillNotWriteIsNeverRendered holds the order in which
// the logger works, across what GORM's four methods do with the six records
// they write: GORM's own level is judged first, and the logger it is held on is
// asked whether it writes the record's level; only when it does is the
// statement rendered, or the message formatted, and only then is the record
// handed over. The handler here does not judge the level again, as the log
// module's level layer does not, so a logger that skipped the question would
// hand its records over all the same.
//
// The held logger is asked for each of the six, at the level the record would be
// written at, whatever it answers; what it answers decides the rest. With a
// level above everything it never sees a record and nothing is rendered; at
// Info, a statement that ran as any other, which is written at Info, is the first
// to go, then the warnings, and last the failures.
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

// contextKey is the key of the value the contexts the cases hand to the logger
// carry.
type contextKey struct{}

// TestANilContextIsTolerated holds all four methods GORM calls to accepting a
// nil context, as GORM's own logger does, and to replacing it with
// context.Background() before it reaches the handler: neither Enabled nor
// Handle of a handler promises to accept a nil one. It holds a context that is
// not nil to being handed on as it is, so the held logger's handler can read
// what the caller put in it.
//
// The nil is a variable of the interface type and not the literal, which
// staticcheck refuses as an argument.
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

// TestAStatementLoggerOverNoLoggerPanics holds the constructor to refusing a
// nil logger with a message. A statement logger that wrote nowhere would let a
// handle work and say nothing, and one that fell back to a logger of its own
// would write where the host never configured; the one place that builds it is
// handed a logger by every caller, so a nil one is a defect in this repository's
// own code and is reported where it was made, not as a nil dereference at the
// first statement.
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

// refusingHandler is a handler that writes no record at any level.
type refusingHandler struct{}

func (refusingHandler) Enabled(context.Context, slog.Level) bool { return false }

func (refusingHandler) Handle(context.Context, slog.Record) error {
	panic("refusingHandler: the logger handed a record to a handler that refused its level")
}

func (h refusingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h refusingHandler) WithGroup(string) slog.Handler      { return h }

// TestARecordTheHeldLoggerWillNotWriteCostsNoAllocation holds the point of
// asking the held logger before the record is built: a statement or a message
// that the held logger would not write costs the caller nothing. Reading the
// call site and rendering the statement are what allocate, and both come after
// the question. The handler refuses every level, and every method GORM calls is
// run on each path that reaches the question: a statement that failed, one
// slower than the threshold and one that ran as any other, and each of GORM's
// three messages.
//
// The statement function and the data of a message are built once, outside the
// measured calls, so that what is counted is the logger's own work. The
// question is Enabled of the held logger and nothing is rendered to answer it.
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

// attributeOrder is the keys of the attributes a record carries, in the order it
// carries them.
func attributeOrder(record slog.Record) []string {
	var keys []string
	record.Attrs(func(attr slog.Attr) bool {
		keys = append(keys, attr.Key)
		return true
	})
	return keys
}

// TestEveryKindOfRecordCarriesItsAttributesInOrder holds the order the
// attributes of each record are written in, which a text destination shows as
// the order of the pairs on a line: what distinguishes the record first (the
// classes of a failure, the threshold a slow statement exceeded), then the time
// the statement took, the rows it affected and the statement, and the call site
// after all of them. A record with the call site first, or the rows before the
// time, carries the same set of attributes and reads differently.
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

// TestTheClassesOfAnErrorAreWrittenInAFixedOrder holds the order the classes of
// one error are joined in to the one the logger documents: GORM's sentinels,
// then a context's end, then the engine's code, whatever order the error chain
// itself lists them in. An error that carries all three kinds at once is the one
// that shows it, because the classes of one kind alone have no order to keep.
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

// failingHandler is a handler that keeps what it is handed and reports that it
// could not write it: every Handle returns an error.
type failingHandler struct {
	*recordingHandler
	// err is what Handle returns.
	err error
}

func (h failingHandler) Handle(ctx context.Context, record slog.Record) error {
	_ = h.recordingHandler.Handle(ctx, record)
	return h.err
}

// TestAStatementReportsTheSameToItsCallerWhetherOrNotItsRecordWasWritten holds
// what a statement returns to its caller to being the statement's own result: a
// handler that cannot write a record is the logger's trouble and nobody else's.
// A statement that failed returns its error as it would have with no logger,
// without the handler's error in its chain; one that succeeded returns none;
// nothing panics; and the handler is asked once for the one record each writes.
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

// TestAStatementIsWrittenAsGORMRenderedIt holds the statement attribute to the
// text GORM rendered, whole: spread over lines, padded with space, and longer
// than any cut a tidy-up might make. A migration file's text is written the same
// way, and a log reader finds the statement it is after in it.
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

// panickingHandler is a handler whose Handle panics.
type panickingHandler struct{ *recordingHandler }

func (panickingHandler) Handle(context.Context, slog.Record) error { panic("the handler cannot cope") }

// TestAPanicInTheHandlerIsNotCaught holds the logger to what the documents say of
// a handler that panics: nothing here recovers it, as nothing else that writes a
// record through the same logger does, so it reaches the caller of the statement
// and does not vanish into a record that was never written.
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
