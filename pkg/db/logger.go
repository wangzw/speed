package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
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

// scanFinisher is the run-time name of GORM's Scan finisher, (*DB).Scan: the
// function that hands Trace a statement whose values GORM filled in itself. A
// GORM that renames it or moves the call leaves those statements written with
// their text, and the logging cases fail.
const scanFinisher = "gorm.io/gorm.(*DB).Scan"

// withheldStatement is what a line carries in place of the text of a statement
// run through GORM's Scan finisher: the text has the statement's values in it,
// and the logger writes none.
const withheldStatement = "statement withheld: GORM filled its values in before the logger saw it"

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
// The statement GORM's Scan finisher runs is the exception: it is written
// without its text. The finisher runs the statement on a logger of its own
// whose ParamsFilter hands every value back, so the text it gives Trace here
// has the values filled in, and this logger is never asked. Trace recognises
// that call by its caller (scanFinisher) and writes the line it writes for any
// other statement — the classes of the error, the time, the rows, the call
// site — with withheldStatement where the text would be.
//
// Every line is written with the call site GORM's own logger reports — the
// first frame outside GORM — so each method below asks for it itself: a
// method delegating to GORM's logger would be that frame, and every line would
// name this file.
//
// Apart from a statement the Scan finisher runs, what a statement's text
// carries is written with it: a literal the caller wrote into the SQL, a
// migration file's content. GORM's own messages — a callback replaced, a model
// the migrator cannot parse — are written as GORM formats them.
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
// rows it affected. When the Scan finisher is the caller the text has the
// values filled in: withheldStatement is written in its place, and of what fc
// gives only the rows are used. A Scan whose statement was never built, its
// handle having carried an error before the chain built one, hands over an
// empty text: nothing was withheld, and its line is written without one.
//
// The call site is asked of utils.FileWithLineNum here, not in a helper: it
// skips the frame that calls it, which has to be this one.
func (l statementLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)
	var head string
	switch {
	case err != nil && l.level >= logger.Error:
		head = "[error] " + failureClasses(err)
	case elapsed > l.slow && l.level >= logger.Warn:
		head = fmt.Sprintf("[warn] slow statement >= %v", l.slow)
	case l.level == logger.Info:
		head = "[info]"
	default:
		return
	}
	statement, rows := fc()
	if statement != "" {
		if pc, _, _, ok := runtime.Caller(1); ok && runtime.FuncForPC(pc).Name() == scanFinisher {
			statement = withheldStatement
		}
	}
	l.out.Printf("%s %s [%s] [rows:%s] %s", utils.FileWithLineNum(), head, milliseconds(elapsed), rowCount(rows),
		statement)
}

// ParamsFilter hands GORM the statement's text and no values, so the text it
// writes them into for Trace keeps its placeholders. The Scan finisher asks its
// own recorder instead of this method; see Trace.
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
