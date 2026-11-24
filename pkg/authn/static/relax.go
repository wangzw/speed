package static

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// The directions that relax a check: more failed attempts, a shorter window, a shorter back-off.
const (
	moveDown = "decrease"
	moveUp   = "increase"
)

// relaxing is, for each setting of this module that relaxes a check when it
// moves, the direction it relaxes in. A setting that is not in the table is one
// whose change relaxes nothing and is not watched.
var relaxing = map[string]string{
	freeAttemptsPath: moveUp,
	failWindowPath:   moveDown,
	maxBackoffPath:   moveDown,
}

// relaxedAction is the name of the audit record of a setting moved to a more
// permissive value, and relaxedOutcome its outcome. The record names the key and
// the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// onSettingsChanged subscribes to settings.changed: a change relaxing one of this module's
// settings writes a Warn and a best-effort audit record, and nothing else. The handler is
// idempotent and never fails an event.
func (b *backend) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	direction, watched := relaxing[change.Key]
	if !watched || change.Sensitive {
		return nil
	}
	before, okBefore := numeric(change.Key, change.Old, change.Old == "")
	after, okAfter := numeric(change.Key, change.New, change.Deleted || change.New == "")
	if !okBefore || !okAfter {
		return nil
	}
	loosens := (direction == moveDown && after < before) || (direction == moveUp && after > before)
	if !loosens {
		return nil
	}
	b.logger.Warn(ModuleName+": a setting was moved to a value that checks less than before",
		slog.String("setting", change.Key), slog.String("direction", direction))
	if b.audit == nil {
		return nil
	}
	if err := b.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  change.Key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": direction},
	}); err != nil {
		b.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

// numeric reads the text of a setting's value as a number to compare, in the
// unit of the setting: a count as itself and a duration in nanoseconds. A layer
// that holds no row, or whose row was deleted, is the declared default.
func numeric(key, text string, declared bool) (int64, bool) {
	if declared {
		return declaredNumber(key)
	}
	if d, err := time.ParseDuration(text); err == nil {
		return int64(d), true
	}
	n, err := strconv.ParseInt(text, 10, 64)
	return n, err == nil
}

func declaredNumber(key string) (int64, bool) {
	switch key {
	case freeAttemptsPath:
		return int64(defaults.FreeAttempts), true
	case failWindowPath:
		return int64(defaults.FailWindow), true
	case maxBackoffPath:
		return int64(defaults.MaxBackoff), true
	}
	return 0, false
}
