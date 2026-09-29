package dbsuite

import (
	"errors"
	"os"
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
	boundKey     = "bound-value-unique-key"
	boundNote    = "bound-value-refused-note"
	boundMissing = "bound-value-looked-up"
	readNote     = "bound-value-read-back"
	boundCast    = "bound-value-cast"
	boundDebug   = "bound-value-debugged"
)

// loggedRow is a row of logged_rows.
type loggedRow struct {
	ID     uint
	Secret string
	Note   string
}

func (loggedRow) TableName() string { return "logged_rows" }

// BoundValuesReachNoOutput holds the GORM logger on a handle an engine's
// module opens to what it writes and where: a statement that fails, one that
// finds no row, one whose rows fail to scan, and every statement of a Debug
// session are written to the process's standard output, the stream a host
// reads them on, with their text and without a value any of them binds or
// reads. It also holds the logger to writing them at all, so a logger that
// writes nothing does not pass.
//
// The logger writes to the process's standard output, which only another
// process can read apart from its standard error, so the case has the calling
// test run again alone in a copy of the test binary: locate gives the parent a
// database for the child; rerun starts the copy with the environment variable
// it is handed added, and returns what the copy wrote to each stream, apart;
// open gives the child a handle the engine's module opened on the database.
func BoundValuesReachNoOutput(t *testing.T, locate func(t *testing.T) string,
	rerun func(t *testing.T, variable string) (stdout, stderr []byte, err error),
	open func(t *testing.T, dsn string) *gorm.DB,
) {
	t.Helper()
	if dsn, child := os.LookupEnv(LoggingChild); child {
		issueLoggedStatements(t, open(t, dsn))
		return
	}
	stdout, stderr, err := rerun(t, LoggingChild+"="+locate(t))
	if err != nil {
		t.Fatalf("the statements failed in the child process: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	streams := []struct {
		name   string
		output []byte
	}{{"standard output", stdout}, {"standard error", stderr}}
	for _, stream := range streams {
		for _, value := range []string{boundKey, boundNote, boundMissing, readNote, boundCast, boundDebug} {
			for _, line := range strings.Split(string(stream.output), "\n") {
				if strings.Contains(line, value) {
					t.Errorf("the child's %s carries the value %q: %s", stream.name, value, line)
				}
			}
		}
	}
	if strings.Count(string(stdout), "logged_rows") < 4 {
		t.Errorf("the child's standard output names logged_rows fewer than four times, so the failed, missing, "+
			"unscanned and debugged statements were not all written to standard output:\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	}
	if strings.Contains(string(stderr), "logged_rows") {
		t.Errorf("a statement was written to the child's standard error, and every statement the module's "+
			"logger writes goes to standard output:\nstderr:\n%s", stderr)
	}
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
	var unscanned []struct{ Note int }
	if err := handle.Model(&loggedRow{}).Select("note").Where("id = ?", 1).Find(&unscanned).Error; err == nil {
		t.Error("reading a text note into an integer succeeded, want the scan to fail")
	}
	// PostgreSQL refuses the cast and quotes the value in its message; SQLite
	// casts it to 0, and the statement is not written.
	_ = handle.Exec("SELECT CAST(? AS INTEGER) FROM logged_rows", boundCast).Error
	var debugged []loggedRow
	if err := handle.Debug().Where("secret = ?", boundDebug).Find(&debugged).Error; err != nil {
		t.Errorf("a debugged read failed: %v", err)
	}
}
