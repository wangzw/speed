package sso

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

const dirIncrease = "increase"

const assertionMaxAgePath = ConfigNamespace + "." + assertionMaxAgeKey

var relaxing = map[string]string{
	assertionMaxAgePath: dirIncrease,
}

const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

func (p *product) subscribe() {
	if p.bus == nil || p.store == nil {
		return
	}
	p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
}

func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
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
	if direction != dirIncrease || after <= before {
		return nil
	}
	p.logger.Warn(ModuleName+": a setting was moved to a value that checks less than before",
		slog.String("setting", change.Key), slog.String("direction", direction))
	if p.audit == nil {
		return nil
	}
	if err := p.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  change.Key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": direction},
	}); err != nil {
		p.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

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
	case assertionMaxAgePath:
		return int64(settingDefaults.AssertionMaxAge), true
	}
	return 0, false
}
