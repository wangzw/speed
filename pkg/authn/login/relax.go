package login

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
	dirIncrease     = "increase"
	dirAddMember    = "add-member"
	dirRemoveMember = "remove-member"
	dirToZero       = "to-zero"
	dirRelax        = "relax"
)

var relaxing = map[string]string{
	flowTTLPath:                dirIncrease,
	disabledMethodsPath:        dirRemoveMember,
	deliveryModesPath:          dirAddMember,
	ipLimitPath:                dirToZero,
	passwordResetPath:          dirRelax,
	passwordResetTicketTTLPath: dirIncrease,
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
	if !relaxes(direction, change.Key, change.Old, change.Old == "", change.New, change.Deleted || change.New == "") {
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

// relaxes treats a layer that held no row as the declared default, the strict direction of every watched setting.
func relaxes(direction, key, oldText string, oldEmpty bool, newText string, newEmpty bool) bool {
	switch direction {
	case dirRelax:
		before := oldText
		if oldEmpty {
			before = passwordResetClosed
		}
		after := newText
		if newEmpty {
			after = passwordResetClosed
		}
		return before == passwordResetClosed && after == passwordResetOpen
	case dirAddMember:
		return addsMember(listValue(key, oldText, oldEmpty), listValue(key, newText, newEmpty))
	case dirRemoveMember:
		return removesMember(listValue(key, oldText, oldEmpty), listValue(key, newText, newEmpty))
	case dirToZero:
		before, okBefore := numberValue(key, oldText, oldEmpty)
		after, okAfter := numberValue(key, newText, newEmpty)
		return okBefore && okAfter && after == 0 && before != 0
	}
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

// listValue filters the password method out of both sides: it is not a member a deployment can turn off.
func listValue(key, text string, empty bool) []string {
	if empty {
		list, _ := parseList(declaredList(key))
		return withoutPasswordMember(key, list)
	}
	list, err := parseList(text)
	if err != nil {
		return nil
	}
	return withoutPasswordMember(key, list)
}

func withoutPasswordMember(key string, list []string) []string {
	if key != disabledMethodsPath {
		return list
	}
	kept, _ := withoutPassword(list)
	return kept
}

func declaredList(key string) string {
	switch key {
	case disabledMethodsPath:
		return defaults.DisabledMethods
	case deliveryModesPath:
		return defaults.DeliveryModes
	}
	return ""
}

func addsMember(before, after []string) bool {
	held := make(map[string]bool, len(before))
	for _, member := range before {
		held[member] = true
	}
	for _, member := range after {
		if !held[member] {
			return true
		}
	}
	return false
}

func removesMember(before, after []string) bool {
	held := make(map[string]bool, len(after))
	for _, member := range after {
		held[member] = true
	}
	for _, member := range before {
		if !held[member] {
			return true
		}
	}
	return false
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
	case flowTTLPath:
		return int64(defaults.FlowTTL), true
	case ipLimitPath:
		return int64(defaults.IPLimit), true
	case passwordResetTicketTTLPath:
		return int64(defaults.PasswordResetTicketTTL), true
	}
	return 0, false
}
