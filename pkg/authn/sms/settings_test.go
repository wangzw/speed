package sms

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheSettingsReadAsTheirDeclaredDefaultWithoutAStore holds the legal
// assembly: no settings store leaves every setting at its declared default, and
// the allowed table empty.
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

// TestAWrittenSettingIsReadAndAnOutOfDomainOneFallsBack holds the read
// discipline: a written value is served, and a value outside the domain falls
// back to the declared default.
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

// TestAFailedSettingReadFallsBackToTheDefault holds the failure direction: a read
// that errors is the declared default and never a zero value that would refuse or
// admit differently.
func TestAFailedSettingReadFallsBackToTheDefault(t *testing.T) {
	store := newStore()
	store.set(resendIntervalPath, 5*time.Minute)
	store.fail(resendIntervalPath, errors.New("the store is down"))
	r := newReader(store, (&logBuffer{}).logger())

	if got := r.resendInterval(); got != defaults.ResendInterval {
		t.Errorf("a failed read returned %v, want the declared default %v", got, defaults.ResendInterval)
	}
}

// TestTheAllowedTableParsesAndRefusesMalformedText holds the list setting: a JSON
// array of prefixes is served, and text that is not one reads as empty, which
// admits no number.
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

// TestTheGlobalBudgetIsChosenByPurpose holds the three per-purpose keys: the read
// picks the key that matches the purpose.
func TestTheGlobalBudgetIsChosenByPurpose(t *testing.T) {
	store := newStore()
	r := newReader(store, (&logBuffer{}).logger())
	store.set(globalLoginPath, 11)
	store.set(globalMFAPath, 22)
	store.set(globalEnrollPath, 33)

	cases := map[string]int{
		authn.SMSPurposeLogin:  11,
		authn.SMSPurposeMFA:    22,
		authn.SMSPurposeEnroll: 33,
	}
	for purpose, want := range cases {
		if got := r.globalHourly(purpose); got != want {
			t.Errorf("globalHourly(%s) = %d, want %d", purpose, got, want)
		}
	}
}

// TestEverySettingDomainAdmitsItsBoundsAndRefusesTheNeighbours holds each domain
// to its declared range: the minimum and maximum are admitted and the values just
// outside are refused. The bounds are written as literals, so a change to a bound
// constant or to a comparison is caught rather than tracked.
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

// TestTheSettingsSchemaDeclaresEveryKeyUnderTheModuleNamespace holds the
// declaration: the namespace is the module name and every key the design names is
// present.
func TestTheSettingsSchemaDeclaresEveryKeyUnderTheModuleNamespace(t *testing.T) {
	s := settingsSchema()
	if s.Namespace != ModuleName {
		t.Errorf("schema namespace = %q, want %q", s.Namespace, ModuleName)
	}
	want := []string{
		allowedPrefixesKey, codeLengthKey, codeTTLKey, codeAttemptsKey, resendIntervalKey,
		perPhoneHourlyKey, perPhoneDailyKey, globalLoginKey, globalMFAKey, globalEnrollKey,
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
