package invite

import (
	"context"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

const moveUp = "increase"

// The record of a relaxed setting names the key and the direction, never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

func (p *product) onSettingsChanged(ctx context.Context, evt event.Event) error {
	var change settings.Change
	if err := event.DecodePayload(evt.Payload, &change); err != nil {
		return nil
	}
	if change.Sensitive {
		return nil
	}
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

func lifetime(text string, declared bool) (time.Duration, bool) {
	if declared {
		return defaults.TTL, true
	}
	d, err := time.ParseDuration(text)
	return d, err == nil
}

func (p *product) warnRelaxation(key string) {
	p.logger.Warn(ModuleName+": a setting is at a value that checks less than before",
		slog.String("setting", key), slog.String("direction", moveUp))
}

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
