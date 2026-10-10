package dbsuite

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// LoggingChild marks the copy of a test binary BoundValuesReachNoOutput has
// started, and carries the locator that copy runs its statements on.
const LoggingChild = "SPEED_DB_LOGGING_CHILD"

// The values the logged statements bind or read, each one a mark the output
// is searched for. None of them may appear in it.
const (
	boundKey       = "bound-value-unique-key"
	boundNote      = "bound-value-refused-note"
	boundMissing   = "bound-value-looked-up"
	readNote       = "bound-value-read-back"
	boundCast      = "bound-value-cast"
	boundDebug     = "bound-value-debugged"
	boundScanned   = "bound-value-scan-refused"
	boundScanDebug = "bound-value-scan-debugged"
)

// The messages of the records the module's logger writes for a statement, which
// are the words a host reads them by. This package does not import the package
// that holds the logger, so they are spelled out here and the case holds the
// logger to them.
const (
	// messageFailed is the message of a statement that failed, written at ERROR
	// with the classes of its error.
	messageFailed = "statement failed"
	// messageSlow is the message of a statement that ran longer than the
	// logger's threshold, written at WARN.
	messageSlow = "slow statement"
	// messageStatement is the message of every other statement, written at INFO,
	// which only a Debug session asks for.
	messageStatement = "statement"
)

// loggerMessages are the messages of every record the module's logger writes
// about what runs on a handle: its statements, and GORM's own messages.
var loggerMessages = []string{
	messageFailed, messageSlow, messageStatement, "gorm info", "gorm warning", "gorm error",
}

// withheldStatement is how the statement attribute of a record begins where the
// text of a statement GORM's Scan finisher ran would be. It is the start of the
// logger's own text, spelled out here because this package does not import the
// package that holds it.
const withheldStatement = "statement withheld"

// Record is one record in the text form the process default logger writes to
// standard output: a line of key=value pairs, a value that holds a space, an
// equals sign or a quote written as a quoted string.
type Record struct {
	// Line is the record as written.
	Line string
	// Level and Message are the record's level, spelled as the handler spells it
	// (INFO, WARN, ERROR), and its message.
	Level, Message string
	// Attrs holds the record's other attributes by key, the time among them, with
	// their values unquoted.
	Attrs map[string]string
}

// Records reads the records out of what a process wrote to one stream. A line
// that opens with a time attribute is a record, and every other line, the test
// binary's own PASS among them, is not. A line that opens like a record and
// cannot be read as one fails the case, naming the stream: a record dropped
// here would leave every check that looks for it with nothing to look at.
func Records(t testing.TB, stream, output string) []Record {
	t.Helper()
	var records []Record
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.HasPrefix(line, "time=") {
			continue
		}
		record, err := parseRecord(line)
		if err != nil {
			t.Errorf("a line of %s opens like a record and cannot be read as one (%v): %s", stream, err, line)
			continue
		}
		records = append(records, record)
	}
	return records
}

// parseRecord reads one line the standard library's text handler wrote. The
// pairs are separated by single spaces, and a value is a Go quoted string
// exactly when it needs quoting, so a value that starts with a quote is read as
// one and any other value runs to the next space.
func parseRecord(line string) (Record, error) {
	attrs := make(map[string]string)
	for rest := line; rest != ""; {
		key, after, found := strings.Cut(rest, "=")
		if !found || key == "" || strings.ContainsAny(key, " \"") {
			return Record{}, fmt.Errorf("there is no key before the equals sign in %q", rest)
		}
		var value string
		if strings.HasPrefix(after, `"`) {
			quoted, err := strconv.QuotedPrefix(after)
			if err == nil {
				value, err = strconv.Unquote(quoted)
			}
			if err != nil {
				return Record{}, fmt.Errorf("the value of %s is not a quoted string: %w", key, err)
			}
			rest = after[len(quoted):]
			if rest != "" {
				if rest[0] != ' ' {
					return Record{}, fmt.Errorf("the value of %s is followed by %q and not by a space", key, rest[:1])
				}
				rest = rest[1:]
			}
		} else {
			value, rest, _ = strings.Cut(after, " ")
		}
		if _, repeated := attrs[key]; repeated {
			return Record{}, fmt.Errorf("the attribute %s is there twice", key)
		}
		attrs[key] = value
	}
	level, hasLevel := attrs["level"]
	message, hasMessage := attrs["msg"]
	if !hasLevel || !hasMessage {
		return Record{}, errors.New("it carries no level or no message")
	}
	delete(attrs, "level")
	delete(attrs, "msg")
	return Record{Line: line, Level: level, Message: message, Attrs: attrs}, nil
}

// loggedRow is a row of logged_rows.
type loggedRow struct {
	ID     uint
	Secret string
	Note   string
}

func (loggedRow) TableName() string { return "logged_rows" }

// runs is the most times the child is run in one case: a run in which a
// statement the case runs was written as a slow statement is repeated, and the
// last run is judged as it stands.
const runs = 3

// BoundValuesReachNoOutput holds the GORM logger on a handle an engine's
// module opens to what it writes and where.
//
// The child process this case runs the statements in has no logging module in
// its registry, so the module's logger writes through the process default
// logger, whose records arrive on standard output, the stream a host reads
// them on, one line each. The case holds those records to the following.
//
//   - No value a statement binds or reads reaches either stream, not in any
//     line, not in any attribute.
//   - Each statement that is written is exactly one record. A statement that
//     fails is a "statement failed" record at ERROR: the one that is refused
//     for a unique key carries the classes of the refusal, GORM's
//     duplicated-key sentinel and the engine's own code (uniqueCode); the one
//     that finds no row carries the not-found sentinel; one whose rows fail to
//     scan is written, without the error's text, which quotes a value. The
//     statements of a Debug session are "statement" records at INFO, the read
//     with its text and its table name.
//   - The statements GORM's Scan finisher runs are written like any other, with
//     the note that the statement is withheld in place of its text: GORM fills
//     their values into the text before the logger sees it. The one that fails
//     carries the classes of its refusal as well, and the one in a Debug
//     session is a "statement" record.
//   - A statement that succeeds outside a Debug session is not a "statement"
//     record, so a logger that ignores GORM's level does not pass.
//   - Every record the logger writes carries module=<module>, the name the
//     engine's module registers under. Every statement record carries a caller
//     written file:line, and the record of each statement the case runs names a
//     line of the file that runs it.
//   - Nothing the logger writes reaches standard error, and nothing that names
//     the table the statements run on reaches standard output except as a
//     record.
//
// A statement the case runs that takes longer than the logger's threshold is
// written as a "slow statement" record in place of the one the case looks for.
// That says how busy the machine was and nothing about the logger, so the child
// is run again while one of its statements was written that way, runs runs in
// all, and the last run is judged as it stands. A statement the module itself
// runs, a migration among them, is written as slow on a slow machine as well,
// and none of the checks looks at it.
//
// The logger writes to the process's standard output, which only another
// process can read apart from its standard error, so the case has the calling
// test run again alone in a copy of the test binary: uniqueCode is how the
// logger writes the engine's own code for a unique key's refusal, the SQLSTATE
// or the result code; locate gives the parent a database for the child; rerun
// starts the copy with the environment variable it is handed added, and returns
// what the copy wrote to each stream, apart; open gives the child a handle the
// engine's module opened on the database.
func BoundValuesReachNoOutput(t *testing.T, module, uniqueCode string, locate func(t *testing.T) string,
	rerun func(t *testing.T, variable string) (stdout, stderr []byte, err error),
	open func(t *testing.T, dsn string) *gorm.DB,
) {
	t.Helper()
	if dsn, child := os.LookupEnv(LoggingChild); child {
		issueLoggedStatements(t, open(t, dsn))
		return
	}
	var (
		stdout, stderr []byte
		records        []Record
	)
	for run := 1; ; run++ {
		var err error
		stdout, stderr, err = rerun(t, LoggingChild+"="+locate(t))
		if err != nil {
			t.Fatalf("the statements failed in the child process: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		records = Records(t, "the child's standard output", string(stdout))
		if run == runs || !ranSlowly(records) {
			break
		}
		t.Logf("one of the statements the child ran was written as a slow statement, so run %d of %d is repeated",
			run, runs)
	}
	streams := []struct {
		name   string
		output []byte
	}{{"standard output", stdout}, {"standard error", stderr}}
	for _, stream := range streams {
		for _, value := range []string{
			boundKey, boundNote, boundMissing, readNote, boundCast, boundDebug,
			boundScanned, boundScanDebug,
		} {
			for _, line := range strings.Split(string(stream.output), "\n") {
				if strings.Contains(line, value) {
					t.Errorf("the child's %s carries the value %q: %s", stream.name, value, line)
				}
			}
		}
	}
	nothingOnStandardError(t, stderr)
	nothingBesideTheRecords(t, stdout)
	statementsOnStandardOutput(t, module, uniqueCode, records)
	if t.Failed() {
		t.Logf("the child's standard output:\n%s\nthe child's standard error:\n%s", stdout, stderr)
	}
}

// suiteFile is the path of this file, which holds the code that runs the
// statements the case looks for, so it is the file their call sites are lines of.
func suiteFile() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}

// ranSlowly reports whether one of the statements the case runs was written as
// a "slow statement" record. A statement the module runs on its own is not one of
// them: its call site is not a line of this file.
func ranSlowly(records []Record) bool {
	suite := suiteFile()
	return slices.ContainsFunc(records, func(r Record) bool {
		return r.Message == messageSlow && namesLineOf(r.Attrs["caller"], suite)
	})
}

// nothingOnStandardError fails the case when the child's standard error carries
// a record of the logger or names the table the statements ran on.
func nothingOnStandardError(t *testing.T, stderr []byte) {
	t.Helper()
	for _, record := range Records(t, "the child's standard error", string(stderr)) {
		if slices.Contains(loggerMessages, record.Message) {
			t.Errorf("the child's standard error carries a %q record, and every record the module's logger "+
				"writes goes to standard output: %s", record.Message, record.Line)
		}
	}
	if strings.Contains(string(stderr), "logged_rows") {
		t.Errorf("a statement was written to the child's standard error, and every statement the module's "+
			"logger writes goes to standard output:\nstderr:\n%s", stderr)
	}
}

// nothingBesideTheRecords fails the case when a line of the child's standard
// output that is not a record names the table the statements ran on: a
// statement written as a plain line next to its record is not caught by a check
// that reads the records only.
func nothingBesideTheRecords(t *testing.T, stdout []byte) {
	t.Helper()
	for line := range strings.SplitSeq(string(stdout), "\n") {
		if !strings.HasPrefix(line, "time=") && strings.Contains(line, "logged_rows") {
			t.Errorf("the child's standard output carries a line that is not a record and names the table "+
				"the statements ran on, and a statement is written to standard output only as a record: %s", line)
		}
	}
}

// statementsOnStandardOutput holds the records on the child's standard output to
// what each statement issueLoggedStatements runs is written as.
//
// A statement is picked out of the records by what its own record carries, and
// the record has to be the only one that does: a logger that writes a statement
// twice, or writes one the case never asked it to, is told apart from one that
// writes each statement once. The statements that fail are picked out by the
// text they carry, or by the withheld note where the text is withheld, and the
// two statements the note stands for are told apart by their level and message.
// What the module's own statements are written as is not judged, apart from the
// module they are stamped with and the form of their call site.
func statementsOnStandardOutput(t *testing.T, module, uniqueCode string, records []Record) {
	t.Helper()
	if len(records) == 0 {
		t.Error("the child's standard output carries no record at all, so the statements the child ran were " +
			"written nowhere, or in a form this case cannot read")
		return
	}
	suite := suiteFile()
	for _, record := range records {
		if !slices.Contains(loggerMessages, record.Message) {
			continue
		}
		if got := record.Attrs["module"]; got != module {
			t.Errorf("a %q record carries module=%q, want the engine's module %q: %s",
				record.Message, got, module, record.Line)
		}
		switch record.Message {
		case messageFailed, messageSlow, messageStatement:
			if !namesLineOf(record.Attrs["caller"], "") {
				t.Errorf("a %q record carries caller=%q, want a file and a line, written file:line: %s",
					record.Message, record.Attrs["caller"], record.Line)
			}
		}
	}

	text := func(r Record) string { return r.Attrs["statement"] }
	failed := func(r Record) bool { return r.Level == "ERROR" && r.Message == messageFailed }
	debugged := func(r Record) bool { return r.Level == "INFO" && r.Message == messageStatement }
	withheld := func(r Record) bool { return strings.HasPrefix(text(r), withheldStatement) }
	debuggedRead := func(r Record) bool {
		return debugged(r) && strings.Contains(text(r), "logged_rows") && strings.Contains(text(r), "secret = ")
	}
	debuggedScan := func(r Record) bool { return debugged(r) && withheld(r) }

	// found is the one record of a statement the case runs, which has to name a
	// line of this file as its call site: the logger reports the first frame
	// outside GORM, which is the code that runs the statement.
	found := func(what string, match func(Record) bool) (Record, bool) {
		record, ok := exactlyOne(t, what, records, match)
		if ok && !namesLineOf(record.Attrs["caller"], suite) {
			t.Errorf("the record of %s carries caller=%q, want a line of %s, the file that ran the statement: %s",
				what, record.Attrs["caller"], suite, record.Line)
		}
		return record, ok
	}

	// The unique key's refusal is written twice: the insert issued with Create,
	// with its text, and the one run through Scan, without it. Both carry the
	// same classes, the sentinel and the engine's own code.
	refusal := gorm.ErrDuplicatedKey.Error()
	if record, ok := found("the refused Create", func(r Record) bool {
		return failed(r) && strings.Contains(text(r), "INSERT INTO")
	}); ok && !sameClasses(record, refusal, uniqueCode) {
		t.Errorf("the refused Create is written with the classes %q, want %q and %q, the sentinel and the "+
			"engine's own code for the refusal of a unique key: %s",
			record.Attrs["classes"], refusal, uniqueCode, record.Line)
	}
	if record, ok := found("the refused Scan", func(r Record) bool {
		return failed(r) && withheld(r)
	}); ok && !sameClasses(record, refusal, uniqueCode) {
		t.Errorf("the refused Scan statement is written with the classes %q, want %q and %q, though its text is "+
			"withheld: %s", record.Attrs["classes"], refusal, uniqueCode, record.Line)
	}
	if record, ok := found("the lookup that finds no row", func(r Record) bool {
		return failed(r) && strings.Contains(text(r), "secret = ")
	}); ok && !sameClasses(record, gorm.ErrRecordNotFound.Error()) {
		t.Errorf("the lookup that finds no row is written with the classes %q, want %q: %s",
			record.Attrs["classes"], gorm.ErrRecordNotFound.Error(), record.Line)
	}
	found("the read whose rows fail to scan", func(r Record) bool {
		return failed(r) && strings.Contains(text(r), "SELECT") && strings.Contains(text(r), "note")
	})

	found("the debugged read", debuggedRead)
	found("the debugged Scan", debuggedScan)
	for _, record := range records {
		if record.Message == messageStatement && !debuggedRead(record) && !debuggedScan(record) {
			t.Errorf("a statement that no Debug session ran is written as a %q record: %s", messageStatement, record.Line)
		}
	}
}

// exactlyOne is the single record that match accepts. It fails the case, naming
// what the record is of, and reports false when none does or more than one does.
func exactlyOne(t *testing.T, what string, records []Record, match func(Record) bool) (Record, bool) {
	t.Helper()
	var found []Record
	for _, record := range records {
		if match(record) {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Errorf("the child's standard output holds %d records for %s, want one", len(found), what)
		for _, record := range found {
			t.Logf("a record for %s: %s", what, record.Line)
		}
		return Record{}, false
	}
	return found[0], true
}

// sameClasses reports whether the classes of a record are exactly want, in any
// order. They are written joined by a comma and a space, and a record of an
// error that carries none has no classes attribute at all.
func sameClasses(record Record, want ...string) bool {
	classes, carried := record.Attrs["classes"]
	if !carried {
		return len(want) == 0
	}
	got := strings.Split(classes, ", ")
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	return slices.Equal(got, want)
}

// namesLineOf reports whether caller is a line, written file:line, of file, or of
// any file when file is empty.
func namesLineOf(caller, file string) bool {
	colon := strings.LastIndex(caller, ":")
	if colon <= 0 {
		return false
	}
	line, err := strconv.Atoi(caller[colon+1:])
	return err == nil && line > 0 && (file == "" || caller[:colon] == file)
}

// issueLoggedStatements runs, on handle, each statement the logger writes, and
// fails the case unless each one met the outcome that has it written.
func issueLoggedStatements(t *testing.T, handle *gorm.DB) {
	t.Helper()
	if err := handle.Exec(`CREATE TABLE logged_rows (id INTEGER PRIMARY KEY, secret TEXT NOT NULL UNIQUE,
		note TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("creating logged_rows: %v", err)
	}
	if err := handle.Create(&loggedRow{ID: 1, Secret: boundKey, Note: readNote}).Error; err != nil {
		t.Fatalf("writing the first row: %v", err)
	}
	if err := handle.Create(&loggedRow{ID: 2, Secret: boundKey, Note: boundNote}).Error; !errors.Is(err,
		gorm.ErrDuplicatedKey) {
		t.Errorf("a second row under the unique key gave %v, want it refused", err)
	}
	var found loggedRow
	if err := handle.First(&found, "secret = ?", boundMissing).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("looking up a key no row has gave %v, want no row", err)
	}
	// The scan's error quotes the value it could not convert, so output that
	// lacks the value shows the logger did not write the error's text.
	var unscanned []struct{ Note int }
	switch err := handle.Model(&loggedRow{}).Select("note").Where("id = ?", 1).Find(&unscanned).Error; {
	case err == nil:
		t.Error("reading a text note into an integer succeeded, want the scan to fail")
	case !strings.Contains(err.Error(), readNote):
		t.Errorf("the scan's error does not quote the value it could not convert, so output without that "+
			"value says nothing about whether the logger writes the error's text: %v", err)
	}
	// PostgreSQL refuses the cast and quotes the value in its message, as the
	// scan's error does; SQLite casts it to 0, and the statement is not written.
	if err := handle.Exec("SELECT CAST(? AS INTEGER) FROM logged_rows", boundCast).Error; err != nil &&
		!strings.Contains(err.Error(), boundCast) {
		t.Errorf("an engine refused the cast without quoting the value, so output without that value says "+
			"nothing about whether the logger writes the error's text: %v", err)
	}
	var debugged []loggedRow
	if err := handle.Debug().Where("secret = ?", boundDebug).Find(&debugged).Error; err != nil {
		t.Errorf("a debugged read failed: %v", err)
	}
	// A statement the Scan finisher runs has its values filled into the text
	// before the logger sees it: a refused one and one in a Debug session each
	// bind a mark that text would carry.
	var scannedID uint
	if err := handle.Raw("INSERT INTO logged_rows (id, secret, note) VALUES (?, ?, ?) RETURNING id", 3, boundKey,
		boundScanned).Scan(&scannedID).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Errorf("a scanned insert under the unique key gave %v, want it refused", err)
	}
	var scanned []uint
	debugScan := handle.Debug().Raw("SELECT id FROM logged_rows WHERE secret = ?", boundScanDebug)
	if err := debugScan.Scan(&scanned).Error; err != nil {
		t.Errorf("a debugged scan failed: %v", err)
	}
}
