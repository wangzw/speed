package sms

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms/budget"
)

// TestTheSettingsReadAsTheirDeclaredDefaultWithoutAStore checks that settings read as their declared defaults without a store.
func TestTheSettingsReadAsTheirDeclaredDefaultWithoutAStore(t *testing.T) {
	r := newReader(nil, (&logBuffer{}).logger())
	if got := r.codeLength(); got != defaults.CodeLength {
		t.Errorf("code-length = %d, want %d", got, defaults.CodeLength)
	}
	if got := r.codeTTL(); got != defaults.CodeTTL {
		t.Errorf("code-ttl = %v, want %v", got, defaults.CodeTTL)
	}
	if got := r.resendInterval(); got != defaults.ResendInterval {
		t.Errorf("resend-interval = %v, want %v", got, defaults.ResendInterval)
	}
	if got := r.perPhoneHourly(); got != defaults.PerPhoneHourly {
		t.Errorf("per-phone-hourly = %d, want %d", got, defaults.PerPhoneHourly)
	}
	if got := r.allowedPrefixes(); len(got) != 0 {
		t.Errorf("allowed-prefixes = %v, want empty", got)
	}
}

// TestAWrittenSettingIsReadAndAnOutOfDomainOneFallsBack checks that a written setting is read and an out-of-domain one falls back.
func TestAWrittenSettingIsReadAndAnOutOfDomainOneFallsBack(t *testing.T) {
	store := newStore()
	r := newReader(store, (&logBuffer{}).logger())

	store.set(codeLengthPath, 7)
	if got := r.codeLength(); got != 7 {
		t.Errorf("code-length = %d, want 7", got)
	}
	store.set(codeLengthPath, 99)
	if got := r.codeLength(); got != defaults.CodeLength {
		t.Errorf("an out-of-domain code-length read as %d, want the default %d", got, defaults.CodeLength)
	}
}

// TestAFailedSettingReadFallsBackToTheDefault checks that a failed setting read falls back to the default.
func TestAFailedSettingReadFallsBackToTheDefault(t *testing.T) {
	store := newStore()
	store.set(resendIntervalPath, 5*time.Minute)
	store.fail(resendIntervalPath, errors.New("the store is down"))
	r := newReader(store, (&logBuffer{}).logger())

	if got := r.resendInterval(); got != defaults.ResendInterval {
		t.Errorf("a failed read returned %v, want the declared default %v", got, defaults.ResendInterval)
	}
}

// TestTheAllowedTableParsesAndRefusesMalformedText checks that the allowed table parses and refuses malformed text.
func TestTheAllowedTableParsesAndRefusesMalformedText(t *testing.T) {
	store := newStore()
	r := newReader(store, (&logBuffer{}).logger())

	store.set(allowedPrefixesPath, `["+86","+1"]`)
	if got := r.allowedPrefixes(); len(got) != 2 || got[0] != "+86" {
		t.Errorf("allowed-prefixes = %v, want [+86 +1]", got)
	}
	store.set(allowedPrefixesPath, "not json")
	if got := r.allowedPrefixes(); len(got) != 0 {
		t.Errorf("malformed allowed-prefixes read as %v, want empty", got)
	}
}

// TestTheGlobalBudgetIsChosenByPurpose checks that the global budget is chosen by purpose.
func TestTheGlobalBudgetIsChosenByPurpose(t *testing.T) {
	store := newStore()
	r := newReader(store, (&logBuffer{}).logger())
	store.set(globalLoginPath, 11)
	store.set(globalMFAPath, 22)
	store.set(globalEnrollPath, 33)
	store.set(globalResetPath, 44)

	cases := map[string]int{
		authn.SMSPurposeLogin:  11,
		authn.SMSPurposeMFA:    22,
		authn.SMSPurposeEnroll: 33,
		authn.SMSPurposeReset:  44,
	}
	for purpose, want := range cases {
		if got := r.globalHourly(purpose); got != want {
			t.Errorf("globalHourly(%s) = %d, want %d", purpose, got, want)
		}
	}
}

// TestEverySettingDomainAdmitsItsBoundsAndRefusesTheNeighbours checks that every setting domain admits its bounds and refuses its neighbours.
func TestEverySettingDomainAdmitsItsBoundsAndRefusesTheNeighbours(t *testing.T) {
	intCases := []struct {
		name     string
		min, max int
		inDomain func(int) bool
	}{
		{"code-length", 6, 8, codeLengthInDomain},
		{"code-attempts", 1, 10, codeAttemptsInDomain},
		{"per-phone-hourly", 1, 60, perPhoneHourlyInDomain},
		{"per-phone-daily", 1, 200, perPhoneDailyInDomain},
		{"per-source-hourly", 1, 10000, perSourceHourlyInDomain},
		{"per-source-daily", 1, 100000, perSourceDailyInDomain},
		{"per-source-distinct-hourly", 1, 10000, perSourceDistinctHourlyInDomain},
		{"per-source-distinct-daily", 1, 100000, perSourceDistinctDailyInDomain},
		{"per-source-attempt-hourly", 1, 100000, perSourceAttemptHourlyInDomain},
		{"global-hourly", 1, 1000000, globalHourlyInDomain},
		{"per-flow-sends", 1, 10, perFlowSendsInDomain},
		{"per-account-hourly", 1, 60, perAccountHourlyInDomain},
	}
	for _, c := range intCases {
		t.Run(c.name, func(t *testing.T) {
			if !c.inDomain(c.min) || !c.inDomain(c.max) {
				t.Errorf("%s refuses one of its bounds %d..%d", c.name, c.min, c.max)
			}
			if c.inDomain(c.min - 1) {
				t.Errorf("%s admits %d, one below its minimum %d", c.name, c.min-1, c.min)
			}
			if c.inDomain(c.max + 1) {
				t.Errorf("%s admits %d, one above its maximum %d", c.name, c.max+1, c.max)
			}
		})
	}

	durationCases := []struct {
		name     string
		min, max time.Duration
		inDomain func(time.Duration) bool
	}{
		{"code-ttl", time.Minute, 10 * time.Minute, codeTTLInDomain},
		{"resend-interval", 30 * time.Second, 10 * time.Minute, resendIntervalInDomain},
	}
	for _, c := range durationCases {
		t.Run(c.name, func(t *testing.T) {
			if !c.inDomain(c.min) || !c.inDomain(c.max) {
				t.Errorf("%s refuses one of its bounds %v..%v", c.name, c.min, c.max)
			}
			if c.inDomain(c.min - time.Nanosecond) {
				t.Errorf("%s admits a value below its minimum %v", c.name, c.min)
			}
			if c.inDomain(c.max + time.Nanosecond) {
				t.Errorf("%s admits a value above its maximum %v", c.name, c.max)
			}
		})
	}
}

// TestTheSettingsSchemaDeclaresEveryKeyUnderTheModuleNamespace checks that the schema declares every key under the module namespace.
func TestTheSettingsSchemaDeclaresEveryKeyUnderTheModuleNamespace(t *testing.T) {
	s := settingsSchema()
	if s.Namespace != ModuleName {
		t.Errorf("schema namespace = %q, want %q", s.Namespace, ModuleName)
	}
	want := []string{
		allowedPrefixesKey, codeLengthKey, codeTTLKey, codeAttemptsKey, resendIntervalKey,
		perPhoneHourlyKey, perPhoneDailyKey, perSourceHourlyKey, perSourceDailyKey,
		perSourceDistinctHourlyKey, perSourceDistinctDailyKey, perSourceAttemptHourlyKey,
		globalLoginKey, globalMFAKey, globalEnrollKey, globalResetKey,
		globalAttemptLoginKey, globalAttemptMFAKey, globalAttemptEnrollKey, globalAttemptResetKey,
		perFlowSendsKey, perAccountHourlyKey,
	}
	for _, key := range want {
		if _, ok := s.Items[key]; !ok {
			t.Errorf("the settings schema declares no item %q", key)
		}
	}
	if len(s.Items) != len(want) {
		t.Errorf("the settings schema declares %d items, want %d", len(s.Items), len(want))
	}
}

// TestTheCeilingIsReadByFullKey checks that the ceiling is read by its full key.
func TestTheCeilingIsReadByFullKey(t *testing.T) {
	if ceilingHourlyPath != "sms.ceiling-hourly" {
		t.Errorf("the ceiling's path = %q, want sms.ceiling-hourly", ceilingHourlyPath)
	}
}

// TestTheCeilingFallbackIsTheLedgerDefault checks that the ceiling fallback is the ledger default.
func TestTheCeilingFallbackIsTheLedgerDefault(t *testing.T) {
	r := newReader(nil, (&logBuffer{}).logger())
	if got := r.ceilingHourly(); got != budget.DefaultCeilingHourly {
		t.Errorf("the ceiling fallback is %d, want budget.DefaultCeilingHourly = %d",
			got, budget.DefaultCeilingHourly)
	}
	if got, ok := declaredNumber(ceilingHourlyPath); !ok || got != int64(budget.DefaultCeilingHourly) {
		t.Errorf("the relaxation comparison reads the ceiling default as %d (present %v), want %d",
			got, ok, budget.DefaultCeilingHourly)
	}
}

// TestTheNewBudgetDefaultsAreWhatSettingsReport checks that the new budget defaults are what settings report.
func TestTheNewBudgetDefaultsAreWhatSettingsReport(t *testing.T) {
	r := newReader(nil, (&logBuffer{}).logger())
	if got := r.perSourceDistinctHourly(); got != 20 {
		t.Errorf("per-source-distinct-hourly = %d, want 20", got)
	}
	if got := r.perSourceDistinctDaily(); got != 60 {
		t.Errorf("per-source-distinct-daily = %d, want 60", got)
	}
	if got := r.perSourceAttemptHourly(); got != 60 {
		t.Errorf("per-source-attempt-hourly = %d, want 60", got)
	}
	for purpose, want := range map[string]int{
		authn.SMSPurposeLogin:  750,
		authn.SMSPurposeMFA:    450,
		authn.SMSPurposeEnroll: 150,
		authn.SMSPurposeReset:  300,
	} {
		if got := r.globalAttemptHourly(purpose); got != want {
			t.Errorf("global-attempt-hourly-%s = %d, want %d", purpose, got, want)
		}
	}
}

// TestTheNewBudgetKeysMatchTheReaderPaths checks that the new budget keys match the reader paths.
func TestTheNewBudgetKeysMatchTheReaderPaths(t *testing.T) {
	cases := map[string]string{
		perSourceAttemptHourlyPathLiteral:  perSourceAttemptHourlyPath,
		perSourceDistinctHourlyPathLiteral: perSourceDistinctHourlyPath,
		globalAttemptLoginPathLiteral:      globalAttemptLoginPath,
		globalAttemptMFAPathLiteral:        globalAttemptMFAPath,
		globalAttemptEnrollPathLiteral:     globalAttemptEnrollPath,
		globalAttemptResetPathLiteral:      globalAttemptResetPath,
	}
	for literal, path := range cases {
		if literal != path {
			t.Errorf("the literal path %q does not match the reader path %q", literal, path)
		}
	}
}
