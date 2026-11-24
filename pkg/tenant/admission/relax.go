package admission

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

const (
	dirIncrease = "increase"
	dirToFalse  = "to-false"
)

var relaxing = map[string]string{
	requireMFAKey: dirToFalse,
	requireSSOKey: dirToFalse,
	checkTTLKey:   dirIncrease,
}

// The audit record of a relaxed setting names the key and the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

func (l *layer) onSettingsChanged(ctx context.Context, evt event.Event) error {
	if l.store == nil {
		return nil
	}
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
	l.logger.Warn(diagnosticPrefix+ModuleName+": a setting was moved to a value that checks less than before",
		slog.String("setting", change.Key), slog.String("direction", direction))
	if l.audit == nil {
		return nil
	}
	err := l.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  change.Key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": direction},
	})
	if err != nil {
		l.logger.Error(diagnosticPrefix+ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

// relaxes reports whether the change moved the key in its relaxing direction; a layer with no row reads as the declared default.
func relaxes(direction, key, oldText string, oldEmpty bool, newText string, newEmpty bool) bool {
	switch direction {
	case dirToFalse:
		return booleanValue(key, oldText, oldEmpty) && !booleanValue(key, newText, newEmpty)
	case dirIncrease:
		before, okBefore := numberValue(key, oldText, oldEmpty)
		after, okAfter := numberValue(key, newText, newEmpty)
		return okBefore && okAfter && after > before
	}
	return false
}

func booleanValue(key, text string, empty bool) bool {
	if empty {
		v, _ := declaredBool(key)
		return v
	}
	v, err := strconv.ParseBool(text)
	return err == nil && v
}

func declaredBool(key string) (bool, bool) {
	switch key {
	case requireMFAKey:
		return defaults.RequireMFA, true
	case requireSSOKey:
		return defaults.RequireSSO, true
	}
	return false, false
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
	case checkTTLKey:
		return int64(defaults.CheckTTL), true
	}
	return 0, false
}
