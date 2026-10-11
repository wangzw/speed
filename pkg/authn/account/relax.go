package account

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// The directions a setting is moved in that relax a check. A password that has to
// be shorter, a hash that costs less, a back-off that is shorter or starts later:
// each lets a guess or a weak password through that the declared default would
// have refused.
const (
	moveDown = "decrease"
	moveUp   = "increase"
)

// relaxing is, for each setting of this module that relaxes a check when it
// moves, the direction it relaxes in. A setting that is not in the table is one
// whose change relaxes nothing and is not watched.
var relaxing = map[string]string{
	minLengthPath:      moveDown,
	argonMemoryPath:    moveDown,
	argonIterationPath: moveDown,
	pbkdf2IterPath:     moveDown,
	freeAttemptsPath:   moveUp,
	maxDelayPath:       moveDown,
	decayPath:          moveDown,
}

// relaxedAction is the name of the audit record of a setting moved to a more
// permissive value, and relaxedOutcome its outcome. The record names the key and
// the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// onSettingsChanged is the subscriber of the settings module's change event. A
// change to one of this module's settings that moves it in the direction that
// relaxes a check is a Warn and a best-effort audit record, and nothing else:
// the settings store has no access control of its own and cannot be made to
// refuse, so what this module guarantees is that the change is visible after it.
// The handler is idempotent and never fails an event.
func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	direction, watched := relaxing[change.Key]
	if !watched || change.Sensitive {
		return nil
	}
	before, okBefore := p.numeric(change.Key, change.Old, change.Old == "")
	after, okAfter := p.numeric(change.Key, change.New, change.Deleted || change.New == "")
	if !okBefore || !okAfter {
		return nil
	}
	loosens := (direction == moveDown && after < before) || (direction == moveUp && after > before)
	if !loosens {
		return nil
	}
	p.logger.Warn(ModuleName+": a setting was moved to a value that checks less than before",
		slog.String("setting", change.Key), slog.String("direction", direction))
	if p.audit == nil {
		return nil
	}
	err := p.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  change.Key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": direction},
	})
	if err != nil {
		p.meters.auditFailed(ctx, false)
		p.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slogCause(err))
	}
	return nil
}

// numeric reads the text of a setting's value as a number to compare, in the
// unit of the setting: a count as itself and a duration in nanoseconds. A layer
// that holds no row, or whose row was deleted, is the declared default.
func (p *product) numeric(key, text string, declared bool) (int64, bool) {
	if declared {
		return declaredNumber(key)
	}
	if d, err := time.ParseDuration(text); err == nil {
		return int64(d), true
	}
	n, err := strconv.ParseInt(text, 10, 64)
	return n, err == nil
}

// declaredNumber is the declared default of one of the watched settings, as the
// comparison reads it.
func declaredNumber(key string) (int64, bool) {
	switch key {
	case minLengthPath:
		return int64(defaults.PasswordMinLength), true
	case argonMemoryPath:
		return int64(defaults.PasswordArgon2Memory), true
	case argonIterationPath:
		return int64(defaults.PasswordArgon2Iterations), true
	case pbkdf2IterPath:
		return int64(defaults.PasswordPBKDF2Iterations), true
	case freeAttemptsPath:
		return int64(defaults.ThrottleFreeAttempts), true
	case maxDelayPath:
		return int64(defaults.ThrottleMaxDelay), true
	case decayPath:
		return int64(defaults.ThrottleDecay), true
	}
	return 0, false
}
