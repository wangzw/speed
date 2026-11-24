package session

import (
	"context"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// dirIncrease is the direction a setting of this module is moved in to relax a
// check: every watched setting is a period, and a longer period checks less.
const dirIncrease = "increase"

var relaxing = map[string]string{
	idleTimeoutPath:     dirIncrease,
	absoluteTimeoutPath: dirIncrease,
	tokenAbsolutePath:   dirIncrease,
	ssoAbsolutePath:     dirIncrease,
	checkTTLPath:        dirIncrease,
}

// The name and outcome of the audit record of a setting moved to a more
// permissive value. The record names the key and the direction and never the
// value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// onSettingsChanged turns a change in the relaxing direction into a Warn and a
// best-effort audit record and nothing else: the settings store cannot be made to
// refuse, so what is guaranteed is that the change is visible after the fact.
func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	direction, watched := relaxing[change.Key]
	if !watched || change.Sensitive {
		return nil
	}
	before, okBefore := durationValue(change.Key, change.Old, change.Old == "")
	after, okAfter := durationValue(change.Key, change.New, change.Deleted || change.New == "")
	if !okBefore || !okAfter {
		return nil
	}
	if !loosens(direction, before, after) {
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
		p.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

func loosens(direction string, before, after int64) bool {
	return direction == dirIncrease && after > before
}

// durationValue reads the text of a setting's value as a duration to compare, in
// nanoseconds. A layer that held no row, or whose row was deleted, reads as the
// declared default, which is the strict direction of every watched setting.
func durationValue(key, text string, declared bool) (int64, bool) {
	if declared {
		return declaredNumber(key)
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, false
	}
	return int64(d), true
}

func declaredNumber(key string) (int64, bool) {
	switch key {
	case idleTimeoutPath:
		return int64(defaults.IdleTimeout), true
	case absoluteTimeoutPath:
		return int64(defaults.AbsoluteTimeout), true
	case tokenAbsolutePath:
		return int64(defaults.TokenAbsoluteTimeout), true
	case ssoAbsolutePath:
		return int64(defaults.SSOAbsoluteTimeout), true
	case checkTTLPath:
		return int64(defaults.CheckTTL), true
	}
	return 0, false
}
