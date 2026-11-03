package sms

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms/budget"
)

// The directions a setting is moved in that relax a check. A larger budget, a
// longer-lived code, a shorter interval between sends, a wider allowed table:
// each lets a request through that the declared default would have refused, or
// widens what may be sent to.
const (
	dirIncrease  = "increase"
	dirDecrease  = "decrease"
	dirAddMember = "add-member"
)

// relaxing is, for each setting of this module whose move relaxes a check, the
// direction it relaxes in. A setting absent from the table relaxes nothing and
// is not watched.
var relaxing = map[string]string{
	allowedPrefixesPath: dirAddMember,
	codeLengthPath:      dirDecrease,
	codeTTLPath:         dirIncrease,
	codeAttemptsPath:    dirIncrease,
	resendIntervalPath:  dirDecrease,
	perPhoneHourlyPath:  dirIncrease,
	perPhoneDailyPath:   dirIncrease,
	perSourceHourlyPath: dirIncrease,
	perSourceDailyPath:  dirIncrease,

	perSourceDistinctHourlyPath: dirIncrease,
	perSourceDistinctDailyPath:  dirIncrease,
	perSourceAttemptHourlyPath:  dirIncrease,

	globalLoginPath:         dirIncrease,
	globalMFAPath:           dirIncrease,
	globalEnrollPath:        dirIncrease,
	globalResetPath:         dirIncrease,
	globalAttemptLoginPath:  dirIncrease,
	globalAttemptMFAPath:    dirIncrease,
	globalAttemptEnrollPath: dirIncrease,
	globalAttemptResetPath:  dirIncrease,

	perFlowSendsPath:     dirIncrease,
	perAccountHourlyPath: dirIncrease,

	ceilingHourlyPath: dirIncrease,
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
	p.logger.Warn(ModuleName+": a setting was moved to a value that checks less than before",
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
		p.meters.auditFailed(ctx)
		p.logger.Error(ModuleName+": the audit record of a relaxed setting could not be written",
			slog.String("setting", change.Key), slog.String("cause", err.Error()))
	}
	return nil
}

// relaxes reports whether the change moved the key in its relaxing direction. A
// layer that held no row reads as the declared default, which is the strict
// direction of every watched setting.
func relaxes(direction, key, oldText string, oldEmpty bool, newText string, newEmpty bool) bool {
	if direction == dirAddMember {
		return addsPrefix(prefixesValue(key, oldText, oldEmpty), prefixesValue(key, newText, newEmpty))
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

// prefixesValue reads the text of a list setting as its members, or the declared
// default when the layer held no row.
func prefixesValue(key, text string, empty bool) []string {
	if empty {
		list, _ := parsePrefixes(defaults.AllowedPrefixes)
		return list
	}
	list, err := parsePrefixes(text)
	if err != nil {
		return nil
	}
	return list
}

// addsPrefix reports whether the new table admits a prefix the old one did not.
func addsPrefix(before, after []string) bool {
	held := make(map[string]bool, len(before))
	for _, prefix := range before {
		held[prefix] = true
	}
	for _, prefix := range after {
		if !held[prefix] {
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

// declaredNumber is the declared default of one of the watched settings, as the
// comparison reads it.
func declaredNumber(key string) (int64, bool) {
	switch key {
	case codeLengthPath:
		return int64(defaults.CodeLength), true
	case codeTTLPath:
		return int64(defaults.CodeTTL), true
	case codeAttemptsPath:
		return int64(defaults.CodeAttempts), true
	case resendIntervalPath:
		return int64(defaults.ResendInterval), true
	case perPhoneHourlyPath:
		return int64(defaults.PerPhoneHourly), true
	case perPhoneDailyPath:
		return int64(defaults.PerPhoneDaily), true
	case perSourceHourlyPath:
		return int64(defaults.PerSourceHourly), true
	case perSourceDailyPath:
		return int64(defaults.PerSourceDaily), true
	case perSourceDistinctHourlyPath:
		return int64(defaults.PerSourceDistinctHourly), true
	case perSourceDistinctDailyPath:
		return int64(defaults.PerSourceDistinctDaily), true
	case perSourceAttemptHourlyPath:
		return int64(defaults.PerSourceAttemptHourly), true
	case globalLoginPath:
		return int64(defaults.GlobalLogin), true
	case globalMFAPath:
		return int64(defaults.GlobalMFA), true
	case globalEnrollPath:
		return int64(defaults.GlobalEnroll), true
	case globalResetPath:
		return int64(defaults.GlobalReset), true
	case globalAttemptLoginPath:
		return int64(defaults.GlobalAttemptLogin), true
	case globalAttemptMFAPath:
		return int64(defaults.GlobalAttemptMFA), true
	case globalAttemptEnrollPath:
		return int64(defaults.GlobalAttemptEnroll), true
	case globalAttemptResetPath:
		return int64(defaults.GlobalAttemptReset), true
	case perFlowSendsPath:
		return int64(defaults.PerFlowSends), true
	case perAccountHourlyPath:
		return int64(defaults.PerAccountHourly), true
	case ceilingHourlyPath:
		return int64(budget.DefaultCeilingHourly), true
	}
	return 0, false
}
