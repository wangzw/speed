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

// The direction a setting is moved in that relaxes a check. A longer
// impersonation, or a longer window in which the administrator's recent local
// multi-factor authentication still counts: each lets an impersonation through
// that the declared default would have refused.
const moveUp = "increase"

// relaxing is, for each setting of this module whose move relaxes a check, the
// direction it relaxes in. A setting absent from the table relaxes nothing and is
// not watched.
var relaxing = map[string]string{
	maxTTLPath:     moveUp,
	maxAuthAgePath: moveUp,
}

// relaxedAction is the name of the audit record of a setting moved to a more
// permissive value, and relaxedOutcome its outcome. The record names the key and
// the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// subscribeSettings registers the relaxation watcher on the settings change
// event. It runs in Init, where the bus accepts subscriptions, and only when the
// assembly carries both the event bus and the settings store: either absent is a
// legal assembly with no change to watch and nothing to subscribe with.
func (m *product) subscribeSettings(bus event.Bus) {
	if bus == nil || m.settings.store == nil {
		return
	}
	bus.Subscribe(settings.ChangedEventType, ModuleName, m.onSettingsChanged)
}

// onSettingsChanged is the subscriber of the settings module's change event. A
// change to one of this module's settings that moves it in the direction that
// relaxes a check is a Warn and a best-effort audit record, and nothing else: the
// settings store has no access control of its own and cannot be made to refuse, so
// what this module guarantees is that the change is visible after it. The handler
// is idempotent and never fails an event.
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

// relaxes reports whether the change moved the key in its relaxing direction. A
// layer that held no row reads as the declared default, which is the strict
// direction of every watched setting.
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

// numberValue reads the text of a numeric setting as a number to compare, in the
// unit of the setting: a count as itself and a duration in nanoseconds. A layer
// that held no row reads as the declared default.
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

// declaredNumber is the declared default of one of the watched settings, as the
// comparison reads it.
func declaredNumber(key string) (int64, bool) {
	switch key {
	case maxTTLPath:
		return int64(defaults.MaxTTL), true
	case maxAuthAgePath:
		return int64(defaults.MaxAuthAge), true
	}
	return 0, false
}
