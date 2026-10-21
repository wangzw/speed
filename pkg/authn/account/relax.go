package account

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// The directions a setting is moved in that relax a check. A password that has to
// be shorter, a hash that costs less, a back-off that is shorter or starts later,
// a switch moved to the value that checks less: each lets a guess, a weak
// password or a first login through that the declared default would have
// refused. A hash algorithm moved from the stronger one to the weaker one is a
// downgrade: it does not change how a check is made, but it makes every entry
// written afterwards cheaper to attack.
const (
	moveDown = "decrease"
	moveUp   = "increase"
	// moveRelaxed is the direction of a switch moved to the value that checks
	// less. It is the authn events contract's direction for the relaxation
	// switches, whose value set also includes to-false, to-zero, add-member and
	// remove-member.
	moveRelaxed = "relax"
	// moveDowngrade is the direction of the password algorithm moved from
	// argon2id to pbkdf2-sha256.
	moveDowngrade = "downgrade"
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

// relaxingSwitch is, for each switch of this module that relaxes a check, the
// value it relaxes at. A change to that value from any other one is a
// relaxation; a change back to the strict value is not watched.
var relaxingSwitch = map[string]string{
	signupPath:   signupOpen,
	autoLinkPath: autoLinkVerified,
}

// downgradingSwitch is, for each setting of this module whose value set is
// ordered strongest to weakest, the value that is a downgrade. A move to it from
// any other one is reported; a move back to the stronger value is not.
var downgradingSwitch = map[string]string{
	algorithmPath: string(password.PBKDF2SHA256),
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
	if change.Sensitive {
		return nil
	}
	// Every setting this module watches is read from the global layer alone, so
	// a row written at a tenant layer leaves the effective value unchanged and
	// is not a relaxation.
	if evt.TenantID != "" {
		return nil
	}
	if target, watched := relaxingSwitch[change.Key]; watched {
		if change.New == target && change.Old != target {
			p.reportRelaxation(ctx, change.Key, moveRelaxed)
		}
		return nil
	}
	if target, watched := downgradingSwitch[change.Key]; watched {
		if change.New == target && change.Old != target {
			p.reportRelaxation(ctx, change.Key, moveDowngrade)
		}
		return nil
	}
	direction, watched := relaxing[change.Key]
	if !watched {
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
	p.reportRelaxation(ctx, change.Key, direction)
	return nil
}

// warnRelaxation writes the Warn a removed check is visible by: the key and the
// direction, never the value. The startup diagnostics write it when the value is
// already relaxed, and the change handler writes it when a value is moved there.
func (p *product) warnRelaxation(key, direction string) {
	p.logger.Warn(diagnosticPrefix+ModuleName+": a setting is at a value that checks less than before",
		slog.String("setting", key), slog.String("direction", direction))
}

// reportRelaxation writes the Warn and the best-effort audit record of one
// relaxation.
func (p *product) reportRelaxation(ctx context.Context, key, direction string) {
	p.warnRelaxation(key, direction)
	if p.audit == nil {
		return
	}
	err := p.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": direction},
	})
	if err != nil {
		p.meters.auditFailed(ctx, false)
		p.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", key), slogCause(err))
	}
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
