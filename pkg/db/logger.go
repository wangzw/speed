package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// withheldStatement is what a record carries in place of the text of a
// statement run through GORM's Scan finisher: the text has the statement's
// values in it, and the logger writes none.
const withheldStatement = "statement withheld: GORM filled its values in before the logger saw it"

// statementLogger is the GORM logger every handle this package opens carries.
//
// It writes what GORM's default logger writes, when that logger writes it, as
// records of the slog logger it is built over. Which destinations the records
// reach, in what format, and from which level are that logger's to decide; what
// the records say is this one's.
//
// A statement is written as one of three records. One that failed is "statement
// failed", at Error, with the classes its error carries (classes). One slower
// than slowStatement is "slow statement", at Warn, with the threshold it
// exceeded (threshold). Every other statement is "statement", at Info, which a
// Debug session asks for. All three carry the time the statement took
// (elapsed), the rows it affected (rows, left out when GORM counted none), the
// statement (statement, left out when none was built) and the call site
// (caller). GORM's own messages are "gorm info", "gorm warning" and "gorm
// error", at Info, Warn and Error, and carry GORM's text with its data
// formatted in (detail) and the call site (caller): a callback removed or
// registered twice is a warning, a callback chain that fails to compile and a
// model the migrator cannot parse are errors, and a callback replaced is
// information. That last kind is not written in supported use: the callback
// registry reports through the root handle's own logger, which sits at Warn,
// and no session's level reaches it. GORM's levels and slog's
// correspond one to one, so the statements only a Debug session writes are Info
// records, and a held logger that writes from a level above Info writes none of
// them. The messages are fixed words and whatever varies is an attribute, which
// is where a handler judges what it is handed.
//
// A statement's record leaves out a value the statement binds or reads, the
// text of an error, and the text of a statement GORM's Scan finisher runs. A
// statement is written as GORM renders it for its logger, with its placeholders
// unfilled — GORM renders PostgreSQL's $1 as $1$ there: GORM asks the logger for
// the bound values through ParamsFilter before it writes them into the text it
// hands Trace, and this logger hands back none. A failed statement is written
// with the classes its error carries (failureClasses) in place of its error: the
// error's text can quote a value, as PostgreSQL's refusal of a malformed input
// and database/sql's report of a value it could not scan both do. The error
// itself goes back to the caller, unchanged.
//
// The Scan finisher is the exception to writing a statement's text. It runs the
// statement on a logger of its own whose ParamsFilter hands every value back, so
// the text it gives Trace here has the values filled in, and this logger is
// never asked. Trace recognises that call by its caller (scanFinisher) and
// writes the record it writes for any other statement — the classes of the
// error, the time, the rows, the call site — with withheldStatement where the
// text would be.
//
// Every record carries the call site GORM's own logger reports, the first frame
// outside GORM, as its program counter, which a handler that adds the source to
// its output reads, and as the attribute caller, because the chain the log
// module assembles does not add it. Each method GORM calls asks for the frame
// itself, and none leaves it to a helper: the lookup skips the frame it is
// called from, so a helper's would report the method that called it, and every
// record would name this file.
//
// A record the held logger would not write is never rendered, with one
// exception: the Scan finisher has the dialect render the statement's text with
// its values in it before the logger is asked anything, and the record that
// follows drops that text. In every other path, in every method GORM calls,
// GORM's own level is judged first, then the held logger is asked whether it
// writes the record's level, and only then is the call site read and, in Trace,
// the statement rendered. The question has to be asked here: the log module's
// level layer does not judge the level again when it is handed a record.
//
// Apart from a statement the Scan finisher runs, what a statement's text
// carries is written with it: a literal the caller wrote into the SQL, a
// migration file's content. GORM's own messages are not held to the three
// guarantees above, which are those of a statement's record: they are written
// as GORM formats them, with its data in them — the error text GORM reports
// and, for a model the migrator cannot parse, the model value the caller handed
// over, with the reason.
//
// The statements of this type are not the only way GORM reaches standard
// output, because routes of GORM write without any handle's logger: the
// schema parser, which reports a model it cannot parse and a mistyped hook
// through logger.Default; the recorder GORM puts in the handle's place while
// the Scan finisher runs, whose Warn and Error are logger.Default's, which
// writes from Warn up; and
// the migrator under DryRun, which prints the statements it would run with
// fmt.Println. None of them consults the log configuration or the redaction.
type statementLogger struct {
	// out is the logger the records are written through.
	out *slog.Logger
	// level is the GORM log level the logger runs at, which LogMode sets. It
	// decides which of GORM's calls are written at all, and is judged before the
	// held logger is asked.
	level logger.LogLevel
	// slow is how long a statement runs before it is written as slow.
	slow time.Duration
}

// The interfaces GORM looks for on its logger.
var (
	_ logger.Interface  = statementLogger{}
	_ gorm.ParamsFilter = statementLogger{}
)

// newStatementLogger is a logger writing through out at GORM's default level.
//
// It panics on a nil logger rather than writing nowhere: the one place that
// builds it, openHandle, is handed a logger by every caller, so a nil one is a
// defect in this repository's own code, and a handle whose statements went
// nowhere would work and say nothing.
func newStatementLogger(out *slog.Logger) statementLogger {
	if out == nil {
		panic("db: a statement logger writes through a logger, and newStatementLogger was handed none")
	}
	return statementLogger{out: out, level: logger.Warn, slow: slowStatement}
}

// LogMode is a copy of the logger at level.
func (l statementLogger) LogMode(level logger.LogLevel) logger.Interface {
	l.level = level
	return l
}

// Info writes one of GORM's informational messages as the record "gorm info".
//
// The call site is asked of utils.CallerFrame here, not in a helper: it skips
// the frame that calls it, which has to be this one.
func (l statementLogger) Info(ctx context.Context, message string, data ...any) {
	if l.level < logger.Info || !l.out.Enabled(ctx, slog.LevelInfo) {
		return
	}
	frame := utils.CallerFrame()
	l.write(ctx, slog.LevelInfo, "gorm info", frame, slog.String("detail", gormDetail(message, data)))
}

// Warn writes one of GORM's warnings as the record "gorm warning".
//
// The call site is asked of utils.CallerFrame here for the reason Info gives.
func (l statementLogger) Warn(ctx context.Context, message string, data ...any) {
	if l.level < logger.Warn || !l.out.Enabled(ctx, slog.LevelWarn) {
		return
	}
	frame := utils.CallerFrame()
	l.write(ctx, slog.LevelWarn, "gorm warning", frame, slog.String("detail", gormDetail(message, data)))
}

// Error writes one of GORM's error messages as the record "gorm error".
//
// The call site is asked of utils.CallerFrame here for the reason Info gives.
func (l statementLogger) Error(ctx context.Context, message string, data ...any) {
	if l.level < logger.Error || !l.out.Enabled(ctx, slog.LevelError) {
		return
	}
	frame := utils.CallerFrame()
	l.write(ctx, slog.LevelError, "gorm error", frame, slog.String("detail", gormDetail(message, data)))
}

// Trace writes a statement GORM has run as the record its outcome and the
// logger's level ask for: "statement failed" for one that failed, with the
// classes of its error; "slow statement" for one slower than the logger's
// threshold; and at Info "statement" for every other. fc gives the statement's
// text, which ParamsFilter left without values, and the rows it affected. When
// the Scan finisher is the caller the text has the values filled in:
// withheldStatement is written in its place, and of what fc gives only the rows
// are used. A Scan whose statement was never built, its handle having carried
// an error before the chain built one, hands over an empty text: nothing was
// withheld, and its record is written without a statement.
//
// The call site is asked of utils.CallerFrame here, not in a helper: it skips
// the frame that calls it, which has to be this one. runtime.Caller counts from
// its own caller as well, so the caller of Trace is read here too, and only when
// there is a text to withhold.
func (l statementLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)
	var (
		level   slog.Level
		message string
	)
	switch {
	case err != nil && l.level >= logger.Error:
		level, message = slog.LevelError, "statement failed"
	case elapsed > l.slow && l.level >= logger.Warn:
		level, message = slog.LevelWarn, "slow statement"
	case l.level == logger.Info:
		level, message = slog.LevelInfo, "statement"
	default:
		return
	}
	if !l.out.Enabled(ctx, level) {
		return
	}
	frame := utils.CallerFrame()
	statement, rows := fc()

	attrs := make([]slog.Attr, 0, 4)
	switch level {
	case slog.LevelError:
		if classes := failureClasses(err); classes != "" {
			attrs = append(attrs, slog.String("classes", classes))
		}
	case slog.LevelWarn:
		attrs = append(attrs, slog.Duration("threshold", l.slow))
	}
	attrs = append(attrs, slog.Duration("elapsed", elapsed))
	if rows != -1 {
		attrs = append(attrs, slog.Int64("rows", rows))
	}
	if statement != "" {
		if pc, _, _, ok := runtime.Caller(1); ok && runtime.FuncForPC(pc).Name() == scanFinisher {
			statement = withheldStatement
		}
		attrs = append(attrs, slog.String("statement", statement))
	}
	l.write(ctx, level, message, frame, attrs...)
}

// ParamsFilter hands GORM the statement's text and no values, so the text it
// writes them into for Trace keeps its placeholders. The Scan finisher asks its
// own recorder instead of this method; see Trace.
func (statementLogger) ParamsFilter(_ context.Context, statement string, _ ...any) (string, []any) {
	return statement, nil
}

// write hands a record to the handler behind the held logger.
//
// The record is built here and not through the logger's own methods, which
// would stamp it with the call site of this file. It carries the frame it is
// given, which is the call site GORM's own logger reports, as its program
// counter, and the attribute caller, which goes after the others. A frame with no program
// counter, which is what utils.CallerFrame returns when no frame outside GORM
// exists, leaves both out. The caller reads the frame, because
// utils.CallerFrame skips the frame that calls it.
//
// A frame's program counter is that of its call instruction, while a record's
// is the return address runtime.Callers reports, one past it. A handler
// resolves a record's with runtime.CallersFrames, which steps back one: handed
// the frame's own, it would land before the call, and where GORM inlined a
// function into the caller it would name that function and not the caller.
//
// A nil ctx is replaced by context.Background(), as the held logger's own
// Enabled does for the question asked before the record is built.
//
// The handler's error is dropped. GORM's logger interface has no way to return
// one, and what a statement reports to its caller does not depend on whether
// the record could be written. A panic in the handler is not caught either:
// nothing else that writes a record through the same logger catches one.
func (l statementLogger) write(ctx context.Context, level slog.Level, message string, frame runtime.Frame,
	attrs ...slog.Attr,
) {
	var pc uintptr
	if frame.PC != 0 {
		pc = frame.PC + 1
	}
	record := slog.NewRecord(time.Now(), level, message, pc)
	record.AddAttrs(attrs...)
	if pc != 0 {
		record.AddAttrs(slog.String("caller", frame.File+":"+strconv.Itoa(frame.Line)))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = l.out.Handler().Handle(ctx, record)
}

// gormDetail is one of GORM's own messages formatted with its data, without
// the newline several of them end in, which a record has no use for.
func gormDetail(message string, data []any) string {
	return strings.TrimSuffix(fmt.Sprintf(message, data...), "\n")
}

// failureClasses is what the logger writes of a failed statement's error: the
// classes it carries whose text is fixed rather than taken from the statement
// — GORM's not-found and constraint sentinels, a context's end, and the
// engine's code for the failure, PostgreSQL's SQLSTATE or SQLite's result code
// — joined by a comma and a space, and empty when it carries none of them.
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
	return strings.Join(classes, ", ")
}
