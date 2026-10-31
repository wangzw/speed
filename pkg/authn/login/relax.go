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

// The directions a setting is moved in that relax a check. A longer-lived flow, a
// login method taken off the disabled list, a delivery mode added, the
// source-address pre-throttle turned off: each lets a request through that the
// declared default would have refused, or widens what a login may ask for.
const (
	dirIncrease     = "increase"
	dirAddMember    = "add-member"
	dirRemoveMember = "remove-member"
	dirToZero       = "to-zero"
	// dirRelax is the direction of an explicit relaxation switch: the switch moved
	// from its strict value to the value that lets a request through. The events
	// contract names this class relax whatever the switch's value type is.
	dirRelax = "relax"
)

// relaxing is, for each setting of this module whose move relaxes a check, the
// direction it relaxes in. A setting absent from the table relaxes nothing and
// is not watched.
var relaxing = map[string]string{
	flowTTLPath:                dirIncrease,
	disabledMethodsPath:        dirRemoveMember,
	deliveryModesPath:          dirAddMember,
	ipLimitPath:                dirToZero,
	passwordResetPath:          dirRelax,
	passwordResetTicketTTLPath: dirIncrease,
}

// relaxedAction is the name of the audit record of a setting moved to a more
// permissive value, and relaxedOutcome its outcome. The record names the key and
// the direction and never the value.
const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

// subscribe registers the settings-change subscriber, and only when both the
// event bus and the settings store are present: a bus with no store publishes a
// change this module holds no setting to judge, and a store with no bus carries
// no change to this module. It runs in Init, where the bus accepts
// subscriptions.
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

// relaxes reports whether the change moved the key in its relaxing direction. A
// layer that held no row reads as the declared default, which is the strict
// direction of every watched setting.
func relaxes(direction, key, oldText string, oldEmpty bool, newText string, newEmpty bool) bool {
	switch direction {
	case dirRelax:
		// An absent layer reads as the declared default, which is the strict value
		// of every switch watched this way.
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

// listValue reads the text of a list setting as its members, or the declared
// default when the layer held no row. The disabled-methods switch has the
// password method filtered out of both sides, since it is not a member the
// deployment can turn off and a raw value that named it is not a relaxation.
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

// withoutPasswordMember drops the password method from a disabled-methods list
// and leaves every other setting's list alone.
func withoutPasswordMember(key string, list []string) []string {
	if key != disabledMethodsPath {
		return list
	}
	kept, _ := withoutPassword(list)
	return kept
}

// declaredList is the declared default of one of the watched list settings, as
// the comparison reads it.
func declaredList(key string) string {
	switch key {
	case disabledMethodsPath:
		return defaults.DisabledMethods
	case deliveryModesPath:
		return defaults.DeliveryModes
	}
	return ""
}

// addsMember reports whether the new list admits a member the old one did not.
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

// removesMember reports whether the old list held a member the new one does not.
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

// declaredNumber is the declared default of one of the watched numeric settings,
// as the comparison reads it.
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
