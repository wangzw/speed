package impersonation

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

const moveUp = "increase"

var relaxing = map[string]string{
	maxTTLPath:     moveUp,
	maxAuthAgePath: moveUp,
}

const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

func (m *product) subscribeSettings(bus event.Bus) {
	if bus == nil || m.settings.store == nil {
		return
	}
	bus.Subscribe(settings.ChangedEventType, ModuleName, m.onSettingsChanged)
}

func (m *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	direction, watched := relaxing[change.Key]
	if !watched || change.Sensitive {
		return nil
	}
	if !relaxes(direction, change.Key, change.Old, change.Old == "", change.New, change.Deleted || change.New == "") {
		return nil
	}
	m.logger.Warn(ModuleName+": a setting was moved to a value that checks less than before",
		slog.String("setting", change.Key), slog.String("direction", direction))
	if m.audit == nil {
		return nil
	}
	if err := m.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  change.Key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": direction},
	}); err != nil {
		m.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

func relaxes(direction, key, oldText string, oldEmpty bool, newText string, newEmpty bool) bool {
	before, okBefore := numberValue(key, oldText, oldEmpty)
	after, okAfter := numberValue(key, newText, newEmpty)
	if !okBefore || !okAfter {
		return false
	}
	if direction == moveUp {
		return after > before
	}
	return after < before
}

func numberValue(key, text string, empty bool) (int64, bool) {
	if empty {
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
	case maxTTLPath:
		return int64(defaults.MaxTTL), true
	case maxAuthAgePath:
		return int64(defaults.MaxAuthAge), true
	}
	return 0, false
}
