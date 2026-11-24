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

// slowStatement is how long a statement runs before it is written as slow: the
// threshold of GORM's default logger.
const slowStatement = 200 * time.Millisecond

// scanFinisher is the run-time name of (*DB).Scan, which hands Trace a statement
// whose values GORM filled in itself; a GORM that renames it or moves the call
// leaves those statements written with their text.
const scanFinisher = "gorm.io/gorm.(*DB).Scan"

const withheldStatement = "statement withheld: GORM filled its values in before the logger saw it"

// statementLogger is the GORM logger every handle this package opens carries: it writes GORM's statements and messages as records of the slog logger it is built over, leaving out a value the statement binds or reads and the text of an error.
type statementLogger struct {
	out   *slog.Logger
	level logger.LogLevel
	slow  time.Duration
}

var (
	_ logger.Interface  = statementLogger{}
	_ gorm.ParamsFilter = statementLogger{}
)

// newStatementLogger panics on a nil logger: a handle whose statements went
// nowhere would work and say nothing.
func newStatementLogger(out *slog.Logger) statementLogger {
	if out == nil {
		panic("db: a statement logger writes through a logger, and newStatementLogger was handed none")
	}
	return statementLogger{out: out, level: logger.Warn, slow: slowStatement}
}

func (l statementLogger) LogMode(level logger.LogLevel) logger.Interface {
	l.level = level
	return l
}

// Info asks the call site of utils.CallerFrame here, not in a helper: it skips
// the frame that calls it, which has to be this one.
func (l statementLogger) Info(ctx context.Context, message string, data ...any) {
	if l.level < logger.Info || !l.out.Enabled(ctx, slog.LevelInfo) {
		return
	}
	frame := utils.CallerFrame()
	l.write(ctx, slog.LevelInfo, "gorm info", frame, slog.String("detail", gormDetail(message, data)))
}

func (l statementLogger) Warn(ctx context.Context, message string, data ...any) {
	if l.level < logger.Warn || !l.out.Enabled(ctx, slog.LevelWarn) {
		return
	}
	frame := utils.CallerFrame()
	l.write(ctx, slog.LevelWarn, "gorm warning", frame, slog.String("detail", gormDetail(message, data)))
}

func (l statementLogger) Error(ctx context.Context, message string, data ...any) {
	if l.level < logger.Error || !l.out.Enabled(ctx, slog.LevelError) {
		return
	}
	frame := utils.CallerFrame()
	l.write(ctx, slog.LevelError, "gorm error", frame, slog.String("detail", gormDetail(message, data)))
}

// Trace reads the call site and the calling function here, not in a helper, for
// the reason Info gives.
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

func (statementLogger) ParamsFilter(_ context.Context, statement string, _ ...any) (string, []any) {
	return statement, nil
}

// write carries the frame it is given as the record's program counter, one past
// the frame's own call instruction, which is what a handler expects.
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

func gormDetail(message string, data []any) string {
	return strings.TrimSuffix(fmt.Sprintf(message, data...), "\n")
}

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
