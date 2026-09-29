package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

// slowStatement is how long a statement runs before the logger writes it as
// slow: the threshold of GORM's default logger.
const slowStatement = 200 * time.Millisecond

// statementLogger is the GORM logger every handle this package opens carries.
//
// It writes what GORM's default logger writes, when and where that logger
// writes it — a statement that failed and one slower than slowStatement at the
// default level, every statement at Info, which a Debug session asks for, to
// standard output through the standard library's log — but no value a
// statement binds or reads. A statement is written as GORM renders it for its
// logger, with its placeholders unfilled — GORM renders PostgreSQL's $1 as $1$
// there: GORM asks the logger for the bound values through ParamsFilter before
// it writes them into the text it hands Trace, and this logger hands back
// none. A failed statement is written with
// the classes its error carries (failureClasses) in place of its error: the
// error's text can quote a value, as PostgreSQL's refusal of a malformed input
// and database/sql's report of a value it could not scan both do. The error
// itself goes back to the caller, unchanged.
//
// Every line is written with the call site GORM's own logger reports — the
// first frame outside GORM — so each method below asks for it itself: a
// method delegating to GORM's logger would be that frame, and every line would
// name this file.
//
// What a statement's text carries is written with it: a literal the caller
// wrote into the SQL, a migration file's content. GORM's own messages — a
// callback replaced, a model the migrator cannot parse — are written as GORM
// formats them.
type statementLogger struct {
	out   *log.Logger
	level logger.LogLevel
	// slow is how long a statement runs before it is written as slow.
	slow time.Duration
}

// The interfaces GORM looks for on its logger.
var (
	_ logger.Interface  = statementLogger{}
	_ gorm.ParamsFilter = statementLogger{}
)

// newStatementLogger is a logger writing to w at GORM's default level.
func newStatementLogger(w io.Writer) statementLogger {
	return statementLogger{out: log.New(w, "", log.LstdFlags), level: logger.Warn, slow: slowStatement}
}

// standardOutput is the logger a handle is opened with.
func standardOutput() statementLogger { return newStatementLogger(os.Stdout) }

// LogMode is a copy of the logger at level.
func (l statementLogger) LogMode(level logger.LogLevel) logger.Interface {
	l.level = level
	return l
}

// Info writes one of GORM's informational messages.
func (l statementLogger) Info(_ context.Context, message string, data ...any) {
	if l.level >= logger.Info {
		l.out.Printf("%s [info] %s", utils.FileWithLineNum(), fmt.Sprintf(message, data...))
	}
}

// Warn writes one of GORM's warnings.
func (l statementLogger) Warn(_ context.Context, message string, data ...any) {
	if l.level >= logger.Warn {
		l.out.Printf("%s [warn] %s", utils.FileWithLineNum(), fmt.Sprintf(message, data...))
	}
}

// Error writes one of GORM's error messages.
func (l statementLogger) Error(_ context.Context, message string, data ...any) {
	if l.level >= logger.Error {
		l.out.Printf("%s [error] %s", utils.FileWithLineNum(), fmt.Sprintf(message, data...))
	}
}

// Trace writes a statement GORM has run: one that failed, with its error's
// classes; one slower than the logger's threshold; and at Info every one. fc
// gives the statement's text, which ParamsFilter left without values, and the
// rows it affected.
func (l statementLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)
	switch {
	case err != nil && l.level >= logger.Error:
		statement, rows := fc()
		l.out.Printf("%s [error] %s [%s] [rows:%s] %s", utils.FileWithLineNum(), failureClasses(err),
			milliseconds(elapsed), rowCount(rows), statement)
	case elapsed > l.slow && l.level >= logger.Warn:
		statement, rows := fc()
		l.out.Printf("%s [warn] slow statement >= %v [%s] [rows:%s] %s", utils.FileWithLineNum(),
			l.slow, milliseconds(elapsed), rowCount(rows), statement)
	case l.level == logger.Info:
		statement, rows := fc()
		l.out.Printf("%s [info] [%s] [rows:%s] %s", utils.FileWithLineNum(), milliseconds(elapsed),
			rowCount(rows), statement)
	}
}

// ParamsFilter hands GORM the statement's text and no values, so the text it
// writes them into for Trace keeps its placeholders.
func (statementLogger) ParamsFilter(_ context.Context, statement string, _ ...any) (string, []any) {
	return statement, nil
}

// failureClasses is what the logger writes for a failed statement's error: the
// classes it carries whose text is fixed rather than taken from the
// statement — GORM's not-found and constraint sentinels, a context's end, and
// the engine's code for the failure, PostgreSQL's SQLSTATE or SQLite's result
// code — and "statement failed" when it carries none of them.
func failureClasses(err error) string {
	var classes []string
	for _, known := range []error{
		gorm.ErrRecordNotFound, gorm.ErrDuplicatedKey, gorm.ErrForeignKeyViolated,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, known) {
			classes = append(classes, known.Error())
		}
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		classes = append(classes, "SQLSTATE "+state.SQLState())
	}
	var code interface{ Code() int }
	if errors.As(err, &code) {
		classes = append(classes, "result code "+strconv.Itoa(code.Code()))
	}
	if len(classes) == 0 {
		return "statement failed"
	}
	return "statement failed: " + strings.Join(classes, ", ")
}

// milliseconds is a duration as GORM's logger writes it.
func milliseconds(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Nanoseconds())/1e6, 'f', 3, 64) + "ms"
}

// rowCount is a statement's affected rows as GORM's logger writes them: "-"
// where GORM counted none.
func rowCount(rows int64) string {
	if rows == -1 {
		return "-"
	}
	return strconv.FormatInt(rows, 10)
}
