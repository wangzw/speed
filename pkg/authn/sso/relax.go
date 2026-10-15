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

// dirIncrease is the direction a setting is moved in that relaxes a check: a
// larger value lets through what the declared default would have refused. A
// wider assertion window is a longer replay window, so raising it relaxes.
const dirIncrease = "increase"

// assertionMaxAgePath is the full path of the one setting of this module whose
// move relaxes a check.
const assertionMaxAgePath = ConfigNamespace + "." + assertionMaxAgeKey

// relaxing is, for each setting of this module that relaxes a check when it
// moves, the direction it relaxes in. A setting absent from the table relaxes
// nothing and is not watched.
var relaxing = map[string]string{
	assertionMaxAgePath: dirIncrease,
}

// The name and outcome of the audit record of a setting moved to a more
// permissive value. The record names the key and the direction and never the
// value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// subscribe registers the settings-change subscription when both the event bus
// and the settings store are present. It runs in Init, where the bus accepts
// subscriptions. With either absent it is silent: without the store there are no
// adjustable settings, so there is no change to watch.
func (p *product) subscribe() {
	if p.bus == nil || p.store == nil {
		return
	}
	p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
}

// onSettingsChanged is the subscriber of the settings module's change event. A
// change to one of this module's settings in the direction that relaxes a check
// is a Warn and a best-effort audit record, and nothing else: the settings store
// has no access control of its own and cannot be made to refuse, so what this
// module guarantees is that the change is visible after the fact. The handler is
// idempotent and never fails an event.
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

// declaredNumber is the declared default of one of the watched settings, as the
// comparison reads it.
func declaredNumber(key string) (int64, bool) {
	switch key {
	case assertionMaxAgePath:
		return int64(settingDefaults.AssertionMaxAge), true
	}
	return 0, false
}
