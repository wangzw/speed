package invite

import (
	"context"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// moveUp is the direction a lifetime moves in when it is lengthened, which
// relaxes the deadline an invitation carries.
const moveUp = "increase"

// relaxedAction is the name of the audit record of a setting moved to a more
// permissive value, and relaxedOutcome its outcome. The record names the key and
// the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// onSettingsChanged is the subscriber of the settings module's change event. A
// lifetime moved to a longer one is a Warn and a best-effort audit record, and
// nothing else: the settings store has no access control of its own and cannot
// be made to refuse, so what this module guarantees is that the change is
// visible after it. The handler is idempotent and never fails an event.
func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	if change.Sensitive {
		return nil
	}
	// The lifetime is read from the global layer alone, so a row written at a
	// tenant layer leaves the effective value unchanged and is not a relaxation.
	if evt.TenantID != "" {
		return nil
	}
	if change.Key != ttlKey {
		return nil
	}
	before, okBefore := lifetime(change.Old, change.Old == "")
	after, okAfter := lifetime(change.New, change.Deleted || change.New == "")
	if !okBefore || !okAfter || after <= before {
		return nil
	}
	p.reportRelaxation(ctx, change.Key)
	return nil
}

// lifetime reads the text of a lifetime setting as a duration. A layer that
// holds no row, or whose row was deleted, is the declared default.
func lifetime(text string, declared bool) (time.Duration, bool) {
	if declared {
		return defaults.TTL, true
	}
	d, err := time.ParseDuration(text)
	return d, err == nil
}

// warnRelaxation writes the Warn a longer lifetime is visible by: the key and the
// direction, never the value.
func (p *product) warnRelaxation(key string) {
	p.logger.Warn(ModuleName+": a setting is at a value that checks less than before",
		slog.String("setting", key), slog.String("direction", moveUp))
}

// reportRelaxation writes the Warn and the best-effort audit record of one
// relaxation.
func (p *product) reportRelaxation(ctx context.Context, key string) {
	p.warnRelaxation(key)
	if p.audit == nil {
		return
	}
	err := p.audit.Record(ctx, audit.Entry{
		Action:  relaxedAction,
		Object:  key,
		Outcome: relaxedOutcome,
		Detail:  map[string]string{"change": moveUp},
	})
	if err != nil {
		p.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", key), slog.String("cause", err.Error()))
	}
}
