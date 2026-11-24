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

const (
	moveDown      = "decrease"
	moveUp        = "increase"
	moveToZero    = "to-zero"
	moveRelaxed   = "relax"
	moveDowngrade = "downgrade"
)

type relaxWhen int

const (
	relaxWhenLarger relaxWhen = iota
	relaxWhenSmaller
	relaxWhenOff
)

var relaxing = map[string]relaxWhen{
	minLengthPath:      relaxWhenSmaller,
	argonMemoryPath:    relaxWhenSmaller,
	argonIterationPath: relaxWhenSmaller,
	pbkdf2IterPath:     relaxWhenSmaller,
	freeAttemptsPath:   relaxWhenLarger,
	maxDelayPath:       relaxWhenSmaller,
	decayPath:          relaxWhenSmaller,
	signupIPLimitPath:  relaxWhenOff,
	signupHourlyPath:   relaxWhenLarger,
}

var relaxingSwitch = map[string]string{
	signupPath:         signupOpen,
	passwordSignupPath: signupOpen,
}

var downgradingSwitch = map[string]string{
	algorithmPath: string(password.PBKDF2SHA256),
}

var relaxingList = map[string]string{
	reservedPath: moveRelaxed,
}

// The record names the key and the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	if change.Sensitive {
		return nil
	}
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
	if direction, watched := relaxingList[change.Key]; watched {
		before := reservedChangeList(change.Old, change.Old == "")
		after := reservedChangeList(change.New, change.Deleted || change.New == "")
		if removesReservedMember(before, after) {
			p.reportRelaxation(ctx, change.Key, direction)
		}
		return nil
	}
	rule, watched := relaxing[change.Key]
	if !watched {
		return nil
	}
	before, okBefore := p.numeric(change.Key, change.Old, change.Old == "")
	after, okAfter := p.numeric(change.Key, change.New, change.Deleted || change.New == "")
	if !okBefore || !okAfter {
		return nil
	}
	direction, loosens := relaxedMove(rule, before, after)
	if !loosens {
		return nil
	}
	p.reportRelaxation(ctx, change.Key, direction)
	return nil
}

func relaxedMove(rule relaxWhen, before, after int64) (string, bool) {
	switch rule {
	case relaxWhenSmaller:
		return moveDown, after < before
	case relaxWhenLarger:
		return moveUp, after > before
	case relaxWhenOff:
		switch {
		case after == 0 && before != 0:
			return moveToZero, true
		case before == 0:
			// Zero is the budget off, the widest value.
			return "", false
		case after > before:
			return moveUp, true
		}
	}
	return "", false
}

func (p *product) warnRelaxation(key, direction string) {
	p.logger.Warn(diagnosticPrefix+ModuleName+": a setting is at a value that checks less than before",
		slog.String("setting", key), slog.String("direction", direction))
}

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
	case signupIPLimitPath:
		return int64(defaults.SignupIPLimit), true
	case signupHourlyPath:
		return int64(defaults.SignupGlobalHourly), true
	}
	return 0, false
}
