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

const (
	dirIncrease  = "increase"
	dirDecrease  = "decrease"
	dirAddMember = "add-member"
)

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

const (
	relaxedAction  = "authn.settings.relaxed"
	relaxedOutcome = "relaxed"
)

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
