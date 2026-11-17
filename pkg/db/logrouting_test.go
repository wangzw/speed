package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/log"
)

// The cases in this file hold where the handles this package delivers write the
// records of their statements: to the logger the log module delivers, named after
// the database module, and to the process default logger when the assembly took
// no log module in. They run through a host, the way a program meets the module,
// because what they observe is which logger the module takes up and at what
// point of the startup, and a callback called by itself shows neither.

// providerName is the name of the module that delivers the log capability in the
// cases that stand a recording logger in for the log module. It sorts after the
// name of the database module on purpose: modules with no dependency between
// them are constructed in the order of their names, so the provider comes ahead
// of the database only when the database module declares the dependency.
const providerName = "zz-log-provider"

// seen is one record a recording handler was handed: its level and message, and
// every attribute it carries, those bound to the logger before it included.
type seen struct {
	level   slog.Level
	message string
	attrs   []slog.Attr
}

// text is the attribute the record carries under key, as text, and the empty
// string when it carries none.
func (s seen) text(key string) string {
	for _, a := range s.attrs {
		if a.Key == key {
			return a.Value.String()
		}
	}
	return ""
}

// count is how many attributes the record carries under key, those bound to the
// logger before it included.
func (s seen) count(key string) int {
	n := 0
	for _, a := range s.attrs {
		if a.Key == key {
			n++
		}
	}
	return n
}

// recorder keeps what the handlers over it are handed, in the order they are
// handed it.
type recorder struct {
	mu      sync.Mutex
	records []seen
}

func (r *recorder) keep(s seen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, s)
}

func (r *recorder) kept() []seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.records)
}

// recording is a slog.Handler over a recorder. It reports every level enabled,
// so that what the statement logger chooses to write is all there is to look at,
// and it opens no group, which the logger under test never does.
type recording struct {
	into  *recorder
	bound []slog.Attr
}

func (recording) Enabled(context.Context, slog.Level) bool { return true }

func (h recording) Handle(_ context.Context, r slog.Record) error {
	attrs := slices.Clone(h.bound)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	h.into.keep(seen{level: r.Level, message: r.Message, attrs: attrs})
	return nil
}

func (h recording) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.bound = append(slices.Clone(h.bound), attrs...)
	return h
}

func (recording) WithGroup(name string) slog.Handler {
	panic(fmt.Sprintf("db_test: the logger under test opened the group %q, and the recording handler keeps none", name))
}

// recordingLogger is an implementation of the log capability over a recording
// handler, and a count of the names it was asked to name a logger after.
type recordingLogger struct {
	into  *recorder
	mu    sync.Mutex
	named []string
}

var _ log.Logger = (*recordingLogger)(nil)

func newRecordingLogger() *recordingLogger { return &recordingLogger{into: new(recorder)} }

// Named hands out a logger carrying the attribute the log module's own Named
// binds, so that a record reads the same through either.
func (l *recordingLogger) Named(name string) *slog.Logger {
	l.mu.Lock()
	l.named = append(l.named, name)
	l.mu.Unlock()
	return slog.New(recording{into: l.into}).With(log.ModuleAttrKey, name)
}

func (l *recordingLogger) Redaction() log.Redaction { return ignoredRules{} }

// names is what Named was asked for, in the order it was asked.
func (l *recordingLogger) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.named)
}

// module is the descriptor that delivers the logger, and it does so from its New:
// the registry constructs it, which is what a provider the host supplies is not.
// An instance supplied for a module exists before any New has run, so it takes
// its place in no order and cannot show what the order is. The capability is not
// claimed exclusively, so two of these can stand side by side.
func (l *recordingLogger) module(name string) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return l, nil },
	}
}

// ignoredRules takes every redaction rule it is handed and keeps none.
type ignoredRules struct{}

func (ignoredRules) AddKeys(...string)              {}
func (ignoredRules) AddPattern(string, log.Matcher) {}

// The statements the cases issue. Every table name begins with the same word,
// which tells a record of these statements from any other the module writes, and
// every bound value is a mark of its own that no output may carry.
const (
	createNotes   = "CREATE TABLE routing_notes (note TEXT NOT NULL)"
	insertNote    = "INSERT INTO routing_notes (note) VALUES (?)"
	insertMissing = "INSERT INTO routing_missing (note) VALUES (?)"
	selectMissing = "SELECT note FROM routing_missing WHERE note = ?"

	failedValue  = "routing-value-failed"
	debugValue   = "routing-value-debugged"
	slowValue    = "routing-value-slow"
	scannedValue = "routing-value-scanned"
)

// routingValues are the values the statements bind, none of which may reach any
// destination.
var routingValues = []string{failedValue, debugValue, slowValue, scannedValue}

// issueStatements runs on handle one statement for each record the logger
// writes, and binds a value of its own in each: an insert that fails, an insert
// in a Debug session, an insert in a session that counts every statement as
// slow, and a select that fails through GORM's Scan finisher. The table they
// work on is created with the logger silenced, so that the setup is not among
// the records. The threshold of the Debug session is raised to an hour, so that
// a machine that stalls cannot turn its statement into a slow one.
func issueStatements(t *testing.T, handle *gorm.DB) {
	t.Helper()
	ctx := t.Context()
	quiet := handle.Session(&gorm.Session{Logger: logger.Discard})
	if err := quiet.Exec(createNotes).Error; err != nil {
		t.Fatalf("creating the table the statements write to: %v", err)
	}
	if err := handle.WithContext(ctx).Exec(insertMissing, failedValue).Error; err == nil {
		t.Error("an insert into a table that does not exist succeeded, and the case needs it to fail")
	}
	if err := db.SlowAfter(handle, time.Hour).Debug().WithContext(ctx).Exec(insertNote, debugValue).Error; err != nil {
		t.Errorf("an insert in a Debug session failed: %v", err)
	}
	if err := db.SlowAfter(handle, time.Nanosecond).WithContext(ctx).Exec(insertNote, slowValue).Error; err != nil {
		t.Errorf("an insert in a session that counts every statement as slow failed: %v", err)
	}
	var note string
	if err := handle.WithContext(ctx).Raw(selectMissing, scannedValue).Scan(&note).Error; err == nil {
		t.Error("a select from a table that does not exist succeeded, and the case needs it to fail")
	}
}

// openedHandle builds a connection through the delivered capability, to a
// database of its own, and releases its pool when the case ends.
func openedHandle(t *testing.T, h *host) *gorm.DB {
	t.Helper()
	opened, err := h.Instance.Open(t.Context(), filepath.Join(t.TempDir(), "opened.db"))
	if err != nil {
		t.Fatalf("building a second connection: %v", err)
	}
	pool := poolOf(t, opened)
	t.Cleanup(func() {
		if closeErr := pool.Close(); closeErr != nil {
			t.Errorf("releasing the second connection's pool: %v", closeErr)
		}
	})
	return opened
}

// failureOf is the one record the recording logger holds for the failed
// statement, which the case has run exactly once.
func failureOf(t *testing.T, provider *recordingLogger, statement string) seen {
	t.Helper()
	var found []seen
	for _, record := range provider.into.kept() {
		if record.message == "statement failed" && record.text("statement") == statement {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the recording logger holds %d records of a failed %q, want one; all it was handed: %+v",
			len(found), statement, provider.into.kept())
	}
	return found[0]
}

// TestTheModuleRequiresTheLogCapabilityOptionally holds the descriptor the Spec
// builds to one optional requirement, the log capability.
//
// Optional, because a database has to start in an assembly that never took
// logging in; declared at all, because without the edge nothing orders the two
// modules but their names, and the database would be constructed ahead of the
// log module and find no logger. A different capability, or a requirement that
// is not optional, is the other two ways the declaration goes wrong.
func TestTheModuleRequiresTheLogCapabilityOptionally(t *testing.T) {
	dbsuite.RequireOptionalLogging(t, "the module the Spec builds", db.NewModule(sqliteSpec()))
}

// TestAConstructedLogProviderIsTheLoggerTheStatementsAreWrittenThrough holds the
// order the dependency exists for: a log provider the registry constructs is
// constructed ahead of the database module, so a failed statement on the
// delivered handle reaches it, under the database module's name, with the module
// attribute bound to the logger once: what the recording handler is handed to
// bind and what the record carries of its own count together.
//
// The provider's name sorts after the database module's, so only the declared
// edge puts it first. Without the edge the database is constructed first, the
// lookup finds no instance, the module falls back to the process default logger
// without a sign, and the recording logger stays empty.
func TestAConstructedLogProviderIsTheLoggerTheStatementsAreWrittenThrough(t *testing.T) {
	provider := newRecordingLogger()
	h, err := startHost(t, sqliteSpec(), hostConfig(t), provider.module(providerName))
	if err != nil {
		t.Fatalf("starting a host with a log provider beside the database module: %v", err)
	}
	if runErr := h.Instance.DB().WithContext(t.Context()).Exec(insertMissing, failedValue).Error; runErr == nil {
		t.Fatal("an insert into a table that does not exist succeeded, and the case needs it to fail")
	}

	record := failureOf(t, provider, insertMissing)
	if record.level != slog.LevelError {
		t.Errorf("the failed statement is written at %v, want %v", record.level, slog.LevelError)
	}
	if got := record.text(log.ModuleAttrKey); got != sqliteSpec().ModuleName {
		t.Errorf("the record carries the module %q, want %q: the logger was not named after the database module",
			got, sqliteSpec().ModuleName)
	}
	if n := record.count(log.ModuleAttrKey); n != 1 {
		t.Errorf("the record carries the module attribute %d times, want once: %+v", n, record.attrs)
	}
}

// TestAHostWithoutALogModuleStartsAndDeliversAWorkingHandle holds the other
// half of the declaration being optional: with no provider of the log capability
// in the registry the startup does not fail, the handle it delivers runs
// statements and reports the errors of those that fail, and the host shuts down.
func TestAHostWithoutALogModuleStartsAndDeliversAWorkingHandle(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t))
	if err != nil {
		t.Fatalf("starting a host with no log module: %v", err)
	}
	handle := h.Instance.DB().WithContext(t.Context())
	if execErr := handle.Exec(createNotes).Error; execErr != nil {
		t.Fatalf("creating a table on the delivered handle: %v", execErr)
	}
	if execErr := handle.Exec(insertNote, debugValue).Error; execErr != nil {
		t.Errorf("writing a row on the delivered handle: %v", execErr)
	}
	// The statement fails on purpose, and the process default logger would
	// write it to the output of the test.
	quiet := handle.Session(&gorm.Session{Logger: logger.Discard})
	if execErr := quiet.Exec(insertMissing, failedValue).Error; execErr == nil {
		t.Error("an insert into a table that does not exist succeeded, so the handle hides the error")
	}
	h.finish(t)
}

// TestTwoProvidersOfTheLogCapabilityFailTheStartup holds that a failed lookup of
// the logger which is not the capability's absence is not taken for it: two
// providers that are not exclusive leave the single-valued lookup unable to
// choose, and the startup fails with that error rather than sending the
// statements to a logger the host did not mean.
//
// The error names the module whose lookup failed, and the startup fails with
// nothing opened: the database file the configuration names does not exist
// afterwards, which it would if the connection had been made ahead of the
// lookup and left unreleased.
func TestTwoProvidersOfTheLogCapabilityFailTheStartup(t *testing.T) {
	first, second := newRecordingLogger(), newRecordingLogger()
	path := filepath.Join(t.TempDir(), "never-opened.db")
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}}}`, path)
	_, err := startHost(t, sqliteSpec(), body, first.module(providerName), second.module(providerName+"-second"))
	if !errors.Is(err, core.ErrAmbiguousProvider) {
		t.Fatalf("starting a host with two providers of the log capability gave %v, want ErrAmbiguousProvider", err)
	}
	if want := sqliteSpec().ModuleName + ": taking up the logger"; !strings.Contains(err.Error(), want) {
		t.Errorf("the startup failed with %q, want the module's own report, which begins %q", err, want)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the database file %s exists after the startup failed on the logger lookup (stat: %v): the "+
			"connection was opened before the lookup, and nothing released it", path, statErr)
	}
}

// TestAHandleFromOpenWritesThroughTheDeliveredHandlesLogger holds that a
// connection built with Open writes its statements through the logger the
// delivered handle's module took up: a failure on it reaches the same recording
// logger, under the same module name, and the logger was named once, for the
// module, rather than once more for each connection built. The module attribute
// is bound to each of the two loggers once: a connection that asked the logger
// for its name again would carry it twice.
func TestAHandleFromOpenWritesThroughTheDeliveredHandlesLogger(t *testing.T) {
	const (
		failingOnDelivered = "INSERT INTO routing_delivered_missing (note) VALUES (?)"
		failingOnOpened    = "INSERT INTO routing_opened_missing (note) VALUES (?)"
	)
	provider := newRecordingLogger()
	h, err := startHost(t, sqliteSpec(), hostConfig(t), provider.module(providerName))
	if err != nil {
		t.Fatalf("starting a host with a log provider beside the database module: %v", err)
	}
	opened := openedHandle(t, h)
	ctx := t.Context()
	if runErr := h.Instance.DB().WithContext(ctx).Exec(failingOnDelivered, failedValue).Error; runErr == nil {
		t.Fatal("an insert into a table that does not exist succeeded on the delivered handle")
	}
	if runErr := opened.WithContext(ctx).Exec(failingOnOpened, failedValue).Error; runErr == nil {
		t.Fatal("an insert into a table that does not exist succeeded on the opened handle")
	}

	for _, statement := range []string{failingOnDelivered, failingOnOpened} {
		record := failureOf(t, provider, statement)
		if got := record.text(log.ModuleAttrKey); got != sqliteSpec().ModuleName {
			t.Errorf("the failure of %q carries the module %q, want %q", statement, got, sqliteSpec().ModuleName)
		}
		if n := record.count(log.ModuleAttrKey); n != 1 {
			t.Errorf("the failure of %q carries the module attribute %d times, want once: the handler was handed "+
				"%+v", statement, n, record.attrs)
		}
	}
	if got, want := provider.names(), []string{sqliteSpec().ModuleName}; !slices.Equal(got, want) {
		t.Errorf("the log capability was asked to name a logger %q, want one logger, named %q, for the "+
			"module and every connection it builds", got, want)
	}
}

// repeatedKeys lists the keys that appear more than once in one object of a JSON
// document, found by walking its tokens. A decode into a map keeps one value
// for a key and says nothing of the others, and a count of a substring takes a
// value that happens to hold the key for a second key; the token stream shows
// each key where the document has it.
func repeatedKeys(line []byte) ([]string, error) {
	type object struct {
		// isObject is false for an array, whose elements are values only.
		isObject bool
		// atKey is whether the next token of an object is a key.
		atKey bool
		seen  map[string]bool
	}
	var (
		stack    []*object
		repeated []string
	)
	valueEnded := func() {
		if n := len(stack); n > 0 && stack[n-1].isObject {
			stack[n-1].atKey = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return repeated, nil
		}
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case json.Delim:
			switch token {
			case '{', '[':
				stack = append(stack, &object{isObject: token == '{', atKey: token == '{', seen: map[string]bool{}})
			default:
				stack = stack[:len(stack)-1]
				valueEnded()
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].isObject && stack[n-1].atKey {
				top := stack[n-1]
				if top.seen[token] {
					repeated = append(repeated, token)
				}
				top.seen[token] = true
				top.atKey = false
				continue
			}
			valueEnded()
		default:
			valueEnded()
		}
	}
}

// TestRepeatedKeysFindsOnlyAKeyThatIsWrittenTwice holds the helper the cases
// below read the log file with to what it claims: a key written twice in one
// object is reported, wherever its two values are, and a value that holds the
// key, the same key in another object, or in an array of objects, is not a
// repetition.
func TestRepeatedKeysFindsOnlyAKeyThatIsWrittenTwice(t *testing.T) {
	for _, c := range []struct {
		name, line string
		want       []string
	}{
		{"no key twice", `{"a":1,"b":"x","c":[1,2]}`, nil},
		{"a value that holds the key", `{"a":"a","b":"\"a\":1"}`, nil},
		{"the same key in two objects", `{"a":{"b":1},"c":{"b":2}}`, nil},
		{"the same key in an array of objects", `{"a":[{"b":1},{"b":2}]}`, nil},
		{"a key twice with scalars", `{"a":1,"b":2,"a":3}`, []string{"a"}},
		{"a key twice after an array value", `{"a":[1,{"x":2}],"a":{"y":3}}`, []string{"a"}},
		{"a key twice inside a nested object", `{"o":{"k":1,"k":2}}`, []string{"k"}},
		{"two keys twice", `{"a":1,"b":2,"b":3,"a":4}`, []string{"b", "a"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := repeatedKeys([]byte(c.line))
			if err != nil {
				t.Fatalf("reading %s: %v", c.line, err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s repeats the keys %q, want %q", c.line, got, c.want)
			}
		})
	}
}

// requireEveryKeyOnce fails the case for each line of the log file in which an
// object carries a key twice, and for a file with no statement record in it, in
// which there would be nothing to hold.
func requireEveryKeyOnce(t *testing.T, file logFile) {
	t.Helper()
	if len(file.statementRecords()) == 0 {
		t.Fatalf("the log file holds no record of a statement, so there is nothing to look at:\n%s", file.raw)
	}
	for _, line := range strings.Split(strings.TrimSpace(file.raw), "\n") {
		repeated, err := repeatedKeys([]byte(line))
		if err != nil {
			t.Fatalf("a line of the log file is not JSON: %v\n%s", err, line)
		}
		if len(repeated) != 0 {
			t.Errorf("a line of the log file carries the keys %q more than once: the logger was stamped with its "+
				"attributes again\n%s", repeated, line)
		}
	}
}

// TestEveryKeyOfARecordIsWrittenOnce holds the file the real chain writes to one
// occurrence of each key in each record, for the delivered handle and for a
// handle from Open. A decode of the line into a map reads a key written twice
// as one, so the bytes of the line are what is looked at.
//
// The module attribute is the one a logger bound twice would repeat: the log
// module binds it when it names a logger, and neither the module that takes the
// logger up nor Open derives its own from it with the attribute once more.
func TestEveryKeyOfARecordIsWrittenOnce(t *testing.T) {
	for _, source := range []struct {
		name   string
		handle func(t *testing.T, h *host) *gorm.DB
	}{
		{"the delivered handle", func(_ *testing.T, h *host) *gorm.DB { return h.Instance.DB() }},
		{"a handle from Open", openedHandle},
	} {
		t.Run(source.name, func(t *testing.T) {
			h, path := startLogged(t, "")
			issueStatements(t, source.handle(t, h))
			file := readLog(t, path)
			requireEveryKeyOnce(t, file)
			for _, record := range file.statementRecords() {
				if module := str(record, log.ModuleAttrKey); module != sqliteSpec().ModuleName {
					t.Errorf("a statement record carries the module %q, want %q", module, sqliteSpec().ModuleName)
				}
			}
		})
	}
}

// startLogged is startHost over the real log module writing JSON to a file of the
// case's own, at level when one is given, and returns the host with the path of
// the file.
func startLogged(t *testing.T, level string, extra ...core.Module) (*host, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log.json")
	section := fmt.Sprintf(`{"outputs":[{"to":"file","format":"json","path":%q}]`, path)
	if level != "" {
		section += fmt.Sprintf(`,"level":%q`, level)
	}
	section += "}"
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}},"log":%s}`, filepath.Join(dir, "host.db"), section)
	h, err := startHost(t, sqliteSpec(), body, append([]core.Module{log.Module()}, extra...)...)
	if err != nil {
		t.Fatalf("starting a host over the log module: %v", err)
	}
	return h, path
}

// logFile is a log file as read back: its text and one decoded record for each
// of its lines.
type logFile struct {
	raw     string
	records []map[string]any
}

// readLog reads the file the log module writes. A record is on the file when the
// statement that produced it has returned, because the destination does not
// buffer.
func readLog(t *testing.T, path string) logFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log file: %v", err)
	}
	file := logFile{raw: string(raw)}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if decodeErr := json.Unmarshal(line, &record); decodeErr != nil {
			t.Fatalf("a line of the log file is not a JSON record: %v\n%s", decodeErr, line)
		}
		file.records = append(file.records, record)
	}
	return file
}

// str is the text a decoded record carries under key, and the empty string when
// it carries none or not text.
func str(record map[string]any, key string) string {
	text, _ := record[key].(string)
	return text
}

// statementMessages are the messages a statement is written under.
var statementMessages = []string{"statement failed", "slow statement", "statement"}

// statementRecords are the records of the log file that are written for the
// statements issueStatements runs: those under a statement message whose call
// site is a line of this file.
//
// The call site is what tells them from the statements the module runs on its
// own, the migrations among them, whose call sites are in the module. A
// statement-text test alone is not enough: the module's Scan statement is
// written as a slow statement on a loaded machine, carries the withheld note
// the case's own Scan statement carries, and would be counted as one of the
// case's.
func (f logFile) statementRecords() []map[string]any {
	var own []map[string]any
	for _, record := range f.records {
		if slices.Contains(statementMessages, str(record, "msg")) &&
			callerOfTheCase.MatchString(str(record, "caller")) {
			own = append(own, record)
		}
	}
	return own
}

// wantedRecord is what the logger writes for one of the statements
// issueStatements runs.
type wantedRecord struct {
	// what names the statement in a failure message.
	what string
	// level and message are the level, as the JSON handler spells it, and the
	// message the record is written under.
	level, message string
	// statement is the text the record carries for the statement.
	statement string
	// failed, slow and counted say which of the attributes classes, threshold
	// and rows the record carries beside the ones every statement record
	// carries. Rows are left out when GORM counted none, and what they hold when
	// counted is rows.
	failed, slow, counted bool
	rows                  float64
}

// wantedRecords are the records of the four statements issueStatements runs, in
// the order it runs them.
var wantedRecords = []wantedRecord{
	{
		what: "the failed insert", level: "ERROR", message: "statement failed", statement: insertMissing,
		failed: true, counted: true, rows: 0,
	},
	{
		what: "the insert in the Debug session", level: "INFO", message: "statement", statement: insertNote,
		counted: true, rows: 1,
	},
	{
		what: "the slow insert", level: "WARN", message: "slow statement", statement: insertNote,
		slow: true, counted: true, rows: 1,
	},
	{
		what: "the select that failed through the Scan finisher", level: "ERROR", message: "statement failed",
		statement: db.WithheldStatement, failed: true,
	},
}

// keys are the attributes a record is written with, the handler's own among
// them: nothing beyond what the design lists, so that an error's text or a value
// has no attribute to arrive in.
func (w wantedRecord) keys() []string {
	keys := []string{"time", "level", "msg", log.ModuleAttrKey, "elapsed", "statement", "caller"}
	if w.failed {
		keys = append(keys, "classes")
	}
	if w.slow {
		keys = append(keys, "threshold")
	}
	if w.counted {
		keys = append(keys, "rows")
	}
	slices.Sort(keys)
	return keys
}

// callerOfTheCase is how a record names a statement issued from this file: the
// call site is the first frame outside GORM.
var callerOfTheCase = regexp.MustCompile(`logrouting_test\.go:\d+$`)

// resultCode is the class an error of the SQLite driver is written under.
var resultCode = regexp.MustCompile(`^result code \d+$`)

// requireStatementRecords holds the log file to holding the records of the
// statements issueStatements runs that kept, indexes into wantedRecords, and
// those alone, each in the shape the design gives it, and to holding none of the
// values the statements bind anywhere.
func requireStatementRecords(t *testing.T, file logFile, kept ...int) {
	t.Helper()
	for _, value := range routingValues {
		if strings.Contains(file.raw, value) {
			t.Errorf("the log file carries the bound value %q:\n%s", value, file.raw)
		}
	}
	own := file.statementRecords()
	if len(own) != len(kept) {
		t.Fatalf("the log file holds %d records for the statements the case issued, want %d:\n%s",
			len(own), len(kept), file.raw)
	}
	for i, index := range kept {
		want, got := wantedRecords[index], own[i]
		if str(got, "level") != want.level || str(got, "msg") != want.message {
			t.Errorf("%s is written as %s %q, want %s %q", want.what, str(got, "level"), str(got, "msg"),
				want.level, want.message)
		}
		if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, want.keys()) {
			t.Errorf("%s is written with the attributes %q, want %q: %v", want.what, keys, want.keys(), got)
		}
		if module := str(got, log.ModuleAttrKey); module != sqliteSpec().ModuleName {
			t.Errorf("%s carries the module %q, want %q", want.what, module, sqliteSpec().ModuleName)
		}
		if statement := str(got, "statement"); statement != want.statement {
			t.Errorf("%s carries the statement %q, want %q", want.what, statement, want.statement)
		}
		if caller := str(got, "caller"); !callerOfTheCase.MatchString(caller) {
			t.Errorf("%s carries the caller %q, want a call site in logrouting_test.go: the first frame "+
				"outside GORM is the one that issued the statement", want.what, caller)
		}
		if _, ok := got["elapsed"].(float64); !ok {
			t.Errorf("%s carries the elapsed time %v, want a number", want.what, got["elapsed"])
		}
		if want.counted && got["rows"] != want.rows {
			t.Errorf("%s counts %v rows, want %v", want.what, got["rows"], want.rows)
		}
		if want.failed && !resultCode.MatchString(str(got, "classes")) {
			t.Errorf("%s carries the classes %q, want the result code of the driver", want.what, got["classes"])
		}
		if want.slow && got["threshold"] != float64(time.Nanosecond) {
			t.Errorf("%s carries the threshold %v, want the %d nanosecond the session gave it", want.what,
				got["threshold"], time.Nanosecond)
		}
	}
}

// TestTheRecordsOfTheStatementsReachTheConfiguredDestination holds the whole
// chain from a statement to a file: with the real log module writing JSON, a
// statement that fails, one in a Debug session, one slower than the threshold and
// one that fails through the Scan finisher each write one record, at the level
// and under the message the design gives them, naming the module, the caller and
// the statement as it was written with its placeholders, and no value any of
// them bound. A connection built with Open writes the same.
func TestTheRecordsOfTheStatementsReachTheConfiguredDestination(t *testing.T) {
	for _, source := range []struct {
		name   string
		handle func(t *testing.T, h *host) *gorm.DB
	}{
		{"the delivered handle", func(_ *testing.T, h *host) *gorm.DB { return h.Instance.DB() }},
		{"a handle from Open", openedHandle},
	} {
		t.Run(source.name, func(t *testing.T) {
			h, path := startLogged(t, "")
			issueStatements(t, source.handle(t, h))
			requireStatementRecords(t, readLog(t, path), 0, 1, 2, 3)
		})
	}
}

// TestAMessageOfGORMsOwnIsWrittenWithItsDetail holds the other kind of record
// the logger writes. A message GORM sends its logger with no statement behind it,
// here the warning for a callback registered twice under one name, is written as
// "gorm warning" at Warn: GORM's text with its data formatted in is the detail
// attribute, without the newline GORM ends it with, beside the module and the
// call site.
func TestAMessageOfGORMsOwnIsWrittenWithItsDetail(t *testing.T) {
	h, path := startLogged(t, "")
	create := h.Instance.DB().Callback().Create()
	for range 2 {
		if err := create.Register("routing:duplicate", func(*gorm.DB) {}); err != nil {
			t.Fatalf("registering a callback: %v", err)
		}
	}

	file := readLog(t, path)
	var warnings []map[string]any
	for _, record := range file.records {
		if str(record, "msg") == "gorm warning" {
			warnings = append(warnings, record)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("the log file holds %d records of a warning from GORM, want one:\n%s", len(warnings), file.raw)
	}
	warning := warnings[0]
	if want := []string{"caller", "detail", "level", log.ModuleAttrKey, "msg", "time"}; !slices.Equal(
		slices.Sorted(maps.Keys(warning)), want) {
		t.Errorf("the warning is written with the attributes %q, want %q: %v",
			slices.Sorted(maps.Keys(warning)), want, warning)
	}
	if str(warning, "level") != "WARN" || str(warning, log.ModuleAttrKey) != sqliteSpec().ModuleName {
		t.Errorf("the warning is written as %s of the module %q, want WARN of %q", str(warning, "level"),
			str(warning, log.ModuleAttrKey), sqliteSpec().ModuleName)
	}
	detail := str(warning, "detail")
	if !strings.HasPrefix(detail, "duplicated callback `routing:duplicate` from ") ||
		!callerOfTheCase.MatchString(detail) {
		t.Errorf("the warning carries the detail %q, want GORM's text with the callback and the call site "+
			"formatted in, and no newline after it", detail)
	}
	if caller := str(warning, "caller"); !callerOfTheCase.MatchString(caller) {
		t.Errorf("the warning carries the caller %q, want a call site in logrouting_test.go", caller)
	}
}

// TestTheConfiguredLevelGovernsWhichStatementsAreWritten holds that the level
// the log configuration gives decides which of the records are written: at warn
// the Debug session's statement, which is an Info record, is gone and the failure
// and the slow statement stay; at error the slow statement goes too.
func TestTheConfiguredLevelGovernsWhichStatementsAreWritten(t *testing.T) {
	// Preparing the log module sets the level of the process default logger to
	// the configured one, so the case ends by preparing it once more at the level
	// a host that configures none gets.
	defer func() {
		startLogged(t, "info")
		if !log.Default().Enabled(t.Context(), slog.LevelInfo) {
			t.Error("the process default logger does not write Info records after the log module was prepared " +
				"at the level a host gets by configuring none")
		}
	}()

	for _, level := range []struct {
		name string
		kept []int
	}{
		{"warn", []int{0, 2, 3}},
		{"error", []int{0, 3}},
	} {
		t.Run(level.name, func(t *testing.T) {
			h, path := startLogged(t, level.name)
			issueStatements(t, h.Instance.DB())
			requireStatementRecords(t, readLog(t, path), level.kept...)
		})
	}
}

// routingChild marks the copy of this test binary a case has started, to observe
// what the process writes on its own streams.
const routingChild = "SPEED_DB_ROUTING_CHILD"

// inChild reports whether this is the copy a case started.
func inChild() bool {
	_, child := os.LookupEnv(routingChild)
	return child
}

// rerun runs the calling test again, alone, in a copy of this test binary whose
// environment says it is the copy, and returns what the copy wrote to its
// standard output and to its standard error, apart.
func rerun(t *testing.T) (stdout, stderr string) {
	t.Helper()
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	child.Env = append(os.Environ(), routingChild+"=1")
	var out, errOut bytes.Buffer
	child.Stdout, child.Stderr = &out, &errOut
	if err := child.Run(); err != nil {
		t.Fatalf("the copy of the test binary failed: %v\nstdout:\n%s\nstderr:\n%s", err, &out, &errOut)
	}
	return out.String(), errOut.String()
}

// requireNoValue fails the case for each of the bound values a stream carries.
func requireNoValue(t *testing.T, name, stream string) {
	t.Helper()
	for _, value := range routingValues {
		if strings.Contains(stream, value) {
			t.Errorf("the child's %s carries the bound value %q:\n%s", name, value, stream)
		}
	}
}

// TestWithoutALogModuleTheStatementsGoToTheProcessLogger holds where the
// statements of a database go in an assembly that took no log module in: to the
// process default logger, which writes records to standard output, each stamped
// with the database module's name because the messages are common words that
// name no writer on their own.
//
// Only another process can read the standard output of this one, so the case
// has the calling test run again in a copy of the test binary and reads what the
// copy wrote.
func TestWithoutALogModuleTheStatementsGoToTheProcessLogger(t *testing.T) {
	if inChild() {
		h, err := startHost(t, sqliteSpec(), hostConfig(t))
		if err != nil {
			t.Fatalf("starting a host with no log module: %v", err)
		}
		issueStatements(t, h.Instance.DB())
		return
	}

	stdout, stderr := rerun(t)
	requireNoValue(t, "standard output", stdout)
	requireNoValue(t, "standard error", stderr)
	module := "module=" + sqliteSpec().ModuleName
	for _, want := range []struct {
		what  string
		marks []string
	}{
		{"the failed insert", []string{"level=ERROR", `msg="statement failed"`, module, quoted("statement", insertMissing)}},
		{"the insert in the Debug session", []string{"level=INFO", "msg=statement ", module, quoted("statement", insertNote)}},
		{"the slow insert", []string{"level=WARN", `msg="slow statement"`, module, quoted("statement", insertNote)}},
		{"the select that failed through the Scan finisher", []string{
			"level=ERROR", `msg="statement failed"`, module, quoted("statement", db.WithheldStatement),
		}},
	} {
		requireOneLine(t, want.what, stdout, want.marks...)
	}
}

// quoted is an attribute as the text handler writes one whose value has a space
// in it.
func quoted(key, value string) string { return key + "=" + fmt.Sprintf("%q", value) }

// requireOneLine fails the case unless the output holds exactly one line that
// carries every one of marks.
func requireOneLine(t *testing.T, what, output string, marks ...string) {
	t.Helper()
	var found []string
lines:
	for _, line := range strings.Split(output, "\n") {
		for _, mark := range marks {
			if !strings.Contains(line, mark) {
				continue lines
			}
		}
		found = append(found, line)
	}
	if len(found) != 1 {
		t.Errorf("the child's standard output holds %d lines for %s, each carrying all of %q, want one:\n%s",
			len(found), what, marks, output)
	}
}

// TestAnEmptyOutputListLeavesTheStatementsUnwritten holds that the destinations
// the log configuration names are the only ones: with the real log module given
// an empty list of outputs, the statements the module writes go nowhere, on
// either of the process's streams.
//
// The log module says out loud, on standard error, that the list is empty, and
// the case holds the copy's standard error to that line, which shows the module
// was assembled empty and that the stream was being read.
func TestAnEmptyOutputListLeavesTheStatementsUnwritten(t *testing.T) {
	if inChild() {
		body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}},"log":{"outputs":[]}}`,
			filepath.Join(t.TempDir(), "host.db"))
		h, err := startHost(t, sqliteSpec(), body, log.Module())
		if err != nil {
			t.Fatalf("starting a host over the log module with no outputs: %v", err)
		}
		issueStatements(t, h.Instance.DB())
		return
	}

	stdout, stderr := rerun(t)
	if !strings.Contains(stderr, "empty output list") {
		t.Errorf("the child's standard error does not say the output list is empty, so the log module was "+
			"not assembled with none, or the stream was not read:\n%s", stderr)
	}
	for name, stream := range map[string]string{"standard output": stdout, "standard error": stderr} {
		requireNoValue(t, name, stream)
		for _, mark := range []string{"routing_", "statement failed", "slow statement"} {
			if strings.Contains(stream, mark) {
				t.Errorf("the child's %s carries %q, so a statement was written with no output to write it to:\n%s",
					name, mark, stream)
				break
			}
		}
	}
}

// occurrences are the spans of value that hold the marker.
func occurrences(value, marker string) []log.Span {
	var spans []log.Span
	for from := 0; from < len(value); {
		at := strings.Index(value[from:], marker)
		if at < 0 {
			break
		}
		start := from + at
		spans = append(spans, log.Span{Start: start, End: start + len(marker)})
		from = start + len(marker)
	}
	return spans
}

// TestRedactionJudgesTheStatementAttribute holds that the text of a statement is
// an attribute of the record, so the redaction the log module applies reaches it:
// a value-shape rule registered through the log capability masks its marker in
// the statement attribute of a record whose statement carries the marker as a
// literal, and leaves a literal no rule covers as it is.
//
// The rules of the log module are process-wide and only ever added to, so the
// marker is one no other case writes. The redaction judges attributes and not the
// message, so a statement written into the message would carry the marker to the
// file.
func TestRedactionJudgesTheStatementAttribute(t *testing.T) {
	const (
		marker  = "routing-redaction-marker-9f3a"
		literal = "routing-plain-literal"
		table   = "routing_redacted_missing"
	)
	registrar := core.Module{
		Name:     "zz-redaction-registrar",
		Requires: []core.Requirement{{Token: (*log.Logger)(nil)}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			delivered, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			delivered.Redaction().AddPattern("db-routing-marker", func(value string) []log.Span {
				return occurrences(value, marker)
			})
			return nil, nil
		},
	}
	h, path := startLogged(t, "", registrar)
	handle := h.Instance.DB().WithContext(t.Context())
	for _, value := range []string{marker, literal} {
		if err := handle.Exec("SELECT '" + value + "' FROM " + table).Error; err == nil {
			t.Fatalf("a select from %s succeeded, and the case needs it to fail", table)
		}
	}

	file := readLog(t, path)
	if strings.Contains(file.raw, marker) {
		t.Errorf("the log file carries the marker a rule masks:\n%s", file.raw)
	}
	var statements []string
	for _, record := range file.statementRecords() {
		statements = append(statements, str(record, "statement"))
	}
	want := []string{
		"SELECT '" + log.MaskedText + "' FROM " + table,
		"SELECT '" + literal + "' FROM " + table,
	}
	if !slices.Equal(statements, want) {
		t.Errorf("the statements the log file carries are %q, want %q: the marker masked where the statement "+
			"was written and the literal no rule covers left as it was", statements, want)
	}
}

// builtWithItsOwnLogging builds a product of the module's own New in a registry
// of its own, in which the real log module writes JSON to the returned file,
// and releases the product's pool and that log module when the case ends.
//
// The log module is constructed by hand and supplied to the registry, because
// the New of the database module takes the logger up from the registry it is
// handed, and a registry that only registers the log module holds no instance
// of it yet.
func builtWithItsOwnLogging(t *testing.T) (db.Database, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "built-log.json")
	locator := filepath.Join(dir, "built.json")
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}},"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`,
		filepath.Join(dir, "built.db"), logPath)
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary configuration source: %v", err)
	}

	module, logging := db.NewModule(sqliteSpec()), log.Module()
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDDBTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(module)
	reg.Register(logging)
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration of the registry the product is built in: %v", err)
	}
	reg.Supply(reader, config.ModuleName)

	delivered, err := logging.New(t.Context(), reg)
	if err != nil {
		t.Fatalf("constructing the log module of the registry the product is built in: %v", err)
	}
	reg.Supply(delivered, log.ModuleName)
	t.Cleanup(func() {
		if closeErr := logging.Close(context.WithoutCancel(t.Context()), reg, delivered); closeErr != nil {
			t.Errorf("releasing the log module of the registry the product is built in: %v", closeErr)
		}
	})

	built, err := module.New(t.Context(), reg)
	if err != nil {
		t.Fatalf("running the module's own New in the registry with its own logging: %v", err)
	}
	product, ok := built.(db.Database)
	if !ok {
		t.Fatalf("the module's New built a %T, which does not deliver the database capability", built)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(product.DB()); closeErr != nil {
			t.Errorf("releasing the pool of the product: %v", closeErr)
		}
	})
	return product, logPath
}

// TestASuppliedProductKeepsTheLoggerOfTheRegistryItWasBuiltIn holds where the
// statements of a product go once it is supplied to another registry: to the log
// destination of the registry whose New built it, and not to that of the one it
// is supplied to. The product is built by the module's own New in a registry
// with a log module writing to one file, and supplied for the module in a host
// whose log module writes to another; a failed statement on the product and one
// on a connection built by its Open each reach the first file and neither
// reaches the second.
//
// What it pins is that the logger is taken up in New and carried by the
// product: the lookup in New, and the handle's statement logger built over its
// result. A product that took the logger of the registry it was supplied to, in
// Migrate or anywhere after New, would write to the second file and leave the
// first empty.
func TestASuppliedProductKeepsTheLoggerOfTheRegistryItWasBuiltIn(t *testing.T) {
	const (
		failingOnProduct = "INSERT INTO routing_supplied_missing (note) VALUES (?)"
		failingOnOpened  = "INSERT INTO routing_supplied_opened_missing (note) VALUES (?)"
	)
	spec := sqliteSpec()
	product, builtLog := builtWithItsOwnLogging(t)

	dir := t.TempDir()
	hostLog := filepath.Join(dir, "host-log.json")
	body := fmt.Sprintf(`{"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`, hostLog)
	h, err := startHostWith(t, spec, body, func(reg *core.Registry) {
		reg.Supply(product, spec.ModuleName)
	}, log.Module())
	if err != nil {
		t.Fatalf("starting a host over a product the module's own New built in another registry: %v", err)
	}
	if h.Instance != product {
		t.Fatalf("the dependant took up %v, want the product that was supplied", h.Instance)
	}

	ctx := t.Context()
	if runErr := product.DB().WithContext(ctx).Exec(failingOnProduct, failedValue).Error; runErr == nil {
		t.Fatal("an insert into a table that does not exist succeeded on the supplied product")
	}
	opened, err := product.Open(ctx, filepath.Join(dir, "opened.db"))
	if err != nil {
		t.Fatalf("building a connection with the supplied product's Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(opened); closeErr != nil {
			t.Errorf("releasing the pool of the connection the product opened: %v", closeErr)
		}
	})
	if runErr := opened.WithContext(ctx).Exec(failingOnOpened, failedValue).Error; runErr == nil {
		t.Fatal("an insert into a table that does not exist succeeded on the connection the product opened")
	}

	var written []string
	for _, record := range readLog(t, builtLog).statementRecords() {
		if str(record, "msg") == "statement failed" {
			written = append(written, str(record, "statement"))
		}
	}
	if want := []string{failingOnProduct, failingOnOpened}; !slices.Equal(written, want) {
		t.Errorf("the log destination of the registry the product was built in holds the failed statements %q, "+
			"want %q", written, want)
	}
	if reached := readLog(t, hostLog).statementRecords(); len(reached) != 0 {
		t.Errorf("the log destination of the registry the product was supplied to holds %d records of the "+
			"product's statements, want none: %v", len(reached), reached)
	}
}

// nilNaming is a provider of the log capability whose Named gives no logger.
type nilNaming struct{ recordingLogger }

func (*nilNaming) Named(string) *slog.Logger { return nil }

// TestAProviderThatNamesNoLoggerFailsTheStartup holds the lookup to reporting a
// provider that hands back no logger as an error naming the module, before any
// connection is made. A panic in the statement logger's constructor is raised on
// the startup's own goroutine, where this test cannot catch it, and ends the test
// binary.
func TestAProviderThatNamesNoLoggerFailsTheStartup(t *testing.T) {
	provider := &nilNaming{*newRecordingLogger()}
	path := filepath.Join(t.TempDir(), "never-opened.db")
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}}}`, path)
	module := core.Module{
		Name:     providerName,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return provider, nil },
	}

	_, err := startHost(t, sqliteSpec(), body, module)
	if err == nil || !strings.Contains(err.Error(), sqliteSpec().ModuleName+": the provider of the log capability gave no logger") {
		t.Fatalf("the startup gave %v, want an error naming the module and the missing logger", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the database file %s exists after the startup failed (stat: %v)", path, statErr)
	}
}
