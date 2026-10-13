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

// The directions a setting is moved in that relax a check. A tenant that stops
// requiring multi-factor or a sign-in through one of its own identity providers,
// and one that remembers a membership and a permission for longer, admits a
// caller the declared default would have refused.
const (
	dirIncrease = "increase"
	dirToFalse  = "to-false"
)

// relaxing is, for each setting of this module that relaxes a check when it
// moves, the direction it relaxes in. A setting absent from the table relaxes
// nothing and is not watched.
var relaxing = map[string]string{
	requireMFAKey: dirToFalse,
	requireSSOKey: dirToFalse,
	checkTTLKey:   dirIncrease,
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
// The handler is idempotent and never fails an event. With no settings store in
// the assembly there is no setting to watch, so the handler is inert.
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

// relaxes reports whether the change moved the key in its relaxing direction. A
// layer that held no row reads as the declared default, which is the strict
// direction of every watched switch and the shortest lifetime.
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

// booleanValue reads the text of a switch as its value, or the declared default
// when the layer held no row.
func booleanValue(key, text string, empty bool) bool {
	if empty {
		v, _ := declaredBool(key)
		return v
	}
	v, err := strconv.ParseBool(text)
	return err == nil && v
}

// declaredBool is the declared default of one of the watched switches, as the
// comparison reads it.
func declaredBool(key string) (bool, bool) {
	switch key {
	case requireMFAKey:
		return defaults.RequireMFA, true
	case requireSSOKey:
		return defaults.RequireSSO, true
	}
	return false, false
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
	case checkTTLKey:
		return int64(defaults.CheckTTL), true
	}
	return 0, false
}
