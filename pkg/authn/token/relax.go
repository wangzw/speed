package token

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// dirIncrease is the direction this module's settings relax in. Every setting it
// watches is a duration whose larger value admits a token or a retry the
// declared default would have refused: a longer-lived access token, a wider
// clock-skew allowance, a longer idle refresh lifetime, a longer reuse window.
const dirIncrease = "increase"

// relaxing is, for each setting of this module whose move relaxes a check, the
// direction it relaxes in. A setting absent from the table relaxes nothing and
// is not watched.
var relaxing = map[string]string{
	accessTTLPath:      dirIncrease,
	leewayPath:         dirIncrease,
	refreshIdleTTLPath: dirIncrease,
	refreshGracePath:   dirIncrease,
}

// The name and outcome of the audit record of a setting moved to a more
// permissive value. The record names the key and the direction and never the
// value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

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
	if !relaxes(direction, change.Key, change.Old, change.Old == "", change.New, change.Deleted || change.New == "") {
		return nil
	}
	p.logger.Warn(diagnosticPrefix+ModuleName+": a setting was moved to a value that checks less than before",
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
		p.recordAuditFailure(ctx)
		p.logger.Error(diagnosticPrefix+ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

// relaxes reports whether the change moved the key in its relaxing direction. A
// layer that held no row reads as the declared default, which is the strict end
// of every watched setting.
func relaxes(direction, key, oldText string, oldEmpty bool, newText string, newEmpty bool) bool {
	before, okBefore := numberValue(key, oldText, oldEmpty)
	after, okAfter := numberValue(key, newText, newEmpty)
	if !okBefore || !okAfter {
		return false
	}
	if direction == dirIncrease {
		return after > before
	}
	return after < before
}

// numberValue reads the text of a watched setting as a number to compare, in the
// unit of the setting: a duration in nanoseconds. A layer that held no row reads
// as the declared default.
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
	case accessTTLPath:
		return int64(defaults.AccessTTL), true
	case leewayPath:
		return int64(defaults.Leeway), true
	case refreshIdleTTLPath:
		return int64(defaults.RefreshIdleTTL), true
	case refreshGracePath:
		return int64(defaults.RefreshGrace), true
	}
	return 0, false
}
