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

// The directions this module's settings relax in: dirIncrease admits a token or a
// retry a larger value would have refused, and dirRelaxed is a switch moved to
// the value that checks less.
const (
	dirIncrease = "increase"
	// dirRelaxed is a switch moved to the value that checks less; the authn events
	// contract also defines to-false, to-zero, add-member and remove-member.
	dirRelaxed = "relax"
)

var relaxing = map[string]string{
	accessTTLPath:      dirIncrease,
	leewayPath:         dirIncrease,
	refreshIdleTTLPath: dirIncrease,
	refreshGracePath:   dirIncrease,
}

// relaxingSwitch is, for each switch of this module that relaxes a check, the
// value it relaxes at. A change to that value from any other one is a
// relaxation; a change back to the strict value is not watched.
var relaxingSwitch = map[string]string{
	checkSessionPath: "false",
}

// The name and outcome of the audit record of a setting moved to a more
// permissive value. The record names the key and the direction and never the
// value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// onSettingsChanged reports, as a Warn and a best-effort audit record, a change
// to one of this module's settings in the direction that relaxes a check. It is
// idempotent and never fails an event.
func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	if change.Sensitive {
		return nil
	}
	// Every watched setting is read from the global layer alone, so a tenant-layer
	// row is not a relaxation.
	if evt.TenantID != "" {
		return nil
	}
	if target, watched := relaxingSwitch[change.Key]; watched {
		if change.New == target && change.Old != target {
			p.reportRelaxation(ctx, change.Key, dirRelaxed)
		}
		return nil
	}
	direction, watched := relaxing[change.Key]
	if !watched {
		return nil
	}
	if !relaxes(direction, change.Key, change.Old, change.Old == "", change.New, change.Deleted || change.New == "") {
		return nil
	}
	p.reportRelaxation(ctx, change.Key, direction)
	return nil
}

// warnRelaxation writes the Warn a removed check is visible by: the key and the
// direction, never the value.
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
		p.recordAuditFailure(ctx)
		p.logger.Error(diagnosticPrefix+ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", key), slog.String("cause", err.Error()))
	}
}

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
