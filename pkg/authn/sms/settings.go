package sms

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms/budget"
)

const (
	allowedPrefixesKey = "allowed-prefixes"
	codeLengthKey      = "code-length"
	codeTTLKey         = "code-ttl"
	codeAttemptsKey    = "code-attempts"
	resendIntervalKey  = "resend-interval"
	perPhoneHourlyKey  = "per-phone-hourly"
	perPhoneDailyKey   = "per-phone-daily"
	perSourceHourlyKey = "per-source-hourly"
	perSourceDailyKey  = "per-source-daily"

	perSourceDistinctHourlyKey = "per-source-distinct-hourly"
	perSourceDistinctDailyKey  = "per-source-distinct-daily"
	perSourceAttemptHourlyKey  = "per-source-attempt-hourly"

	globalLoginKey         = "global-hourly-login"
	globalMFAKey           = "global-hourly-mfa"
	globalEnrollKey        = "global-hourly-enroll"
	globalResetKey         = "global-hourly-reset"
	globalAttemptLoginKey  = "global-attempt-hourly-login"
	globalAttemptMFAKey    = "global-attempt-hourly-mfa"
	globalAttemptEnrollKey = "global-attempt-hourly-enroll"
	globalAttemptResetKey  = "global-attempt-hourly-reset"

	perFlowSendsKey     = "per-flow-sends"
	perAccountHourlyKey = "per-account-hourly"
)

const ceilingHourlyPath = "sms.ceiling-hourly"

var (
	allowedPrefixesPath = ModuleName + "." + allowedPrefixesKey
	codeLengthPath      = ModuleName + "." + codeLengthKey
	codeTTLPath         = ModuleName + "." + codeTTLKey
	codeAttemptsPath    = ModuleName + "." + codeAttemptsKey
	resendIntervalPath  = ModuleName + "." + resendIntervalKey
	perPhoneHourlyPath  = ModuleName + "." + perPhoneHourlyKey
	perPhoneDailyPath   = ModuleName + "." + perPhoneDailyKey
	perSourceHourlyPath = ModuleName + "." + perSourceHourlyKey
	perSourceDailyPath  = ModuleName + "." + perSourceDailyKey

	perSourceDistinctHourlyPath = ModuleName + "." + perSourceDistinctHourlyKey
	perSourceDistinctDailyPath  = ModuleName + "." + perSourceDistinctDailyKey
	perSourceAttemptHourlyPath  = ModuleName + "." + perSourceAttemptHourlyKey

	globalLoginPath         = ModuleName + "." + globalLoginKey
	globalMFAPath           = ModuleName + "." + globalMFAKey
	globalEnrollPath        = ModuleName + "." + globalEnrollKey
	globalResetPath         = ModuleName + "." + globalResetKey
	globalAttemptLoginPath  = ModuleName + "." + globalAttemptLoginKey
	globalAttemptMFAPath    = ModuleName + "." + globalAttemptMFAKey
	globalAttemptEnrollPath = ModuleName + "." + globalAttemptEnrollKey
	globalAttemptResetPath  = ModuleName + "." + globalAttemptResetKey

	perFlowSendsPath     = ModuleName + "." + perFlowSendsKey
	perAccountHourlyPath = ModuleName + "." + perAccountHourlyKey
)

const (
	minCodeLength = 6
	maxCodeLength = 8

	minCodeTTL = time.Minute
	maxCodeTTL = 10 * time.Minute

	minCodeAttempts = 1
	maxCodeAttempts = 10

	minResendInterval = 30 * time.Second
	maxResendInterval = 10 * time.Minute

	minPerPhoneHourly = 1
	maxPerPhoneHourly = 60

	minPerPhoneDaily = 1
	maxPerPhoneDaily = 200

	minPerSourceHourly = 1
	maxPerSourceHourly = 10000

	minPerSourceDaily = 1
	maxPerSourceDaily = 100000

	minPerSourceDistinctHourly = 1
	maxPerSourceDistinctHourly = 10000

	minPerSourceDistinctDaily = 1
	maxPerSourceDistinctDaily = 100000

	minPerSourceAttemptHourly = 1
	maxPerSourceAttemptHourly = 100000

	minGlobalHourly = 1
	maxGlobalHourly = 1000000

	minPerFlowSends = 1
	maxPerFlowSends = 10

	minPerAccountHourly = 1
	maxPerAccountHourly = 60
)

type options struct {
	AllowedPrefixes string        `config:"allowed-prefixes"`
	CodeLength      int           `config:"code-length"`
	CodeTTL         time.Duration `config:"code-ttl"`
	CodeAttempts    int           `config:"code-attempts"`
	ResendInterval  time.Duration `config:"resend-interval"`
	PerPhoneHourly  int           `config:"per-phone-hourly"`
	PerPhoneDaily   int           `config:"per-phone-daily"`
	PerSourceHourly int           `config:"per-source-hourly"`
	PerSourceDaily  int           `config:"per-source-daily"`

	PerSourceDistinctHourly int `config:"per-source-distinct-hourly"`
	PerSourceDistinctDaily  int `config:"per-source-distinct-daily"`
	PerSourceAttemptHourly  int `config:"per-source-attempt-hourly"`

	GlobalLogin         int `config:"global-hourly-login"`
	GlobalMFA           int `config:"global-hourly-mfa"`
	GlobalEnroll        int `config:"global-hourly-enroll"`
	GlobalReset         int `config:"global-hourly-reset"`
	GlobalAttemptLogin  int `config:"global-attempt-hourly-login"`
	GlobalAttemptMFA    int `config:"global-attempt-hourly-mfa"`
	GlobalAttemptEnroll int `config:"global-attempt-hourly-enroll"`
	GlobalAttemptReset  int `config:"global-attempt-hourly-reset"`

	PerFlowSends     int `config:"per-flow-sends"`
	PerAccountHourly int `config:"per-account-hourly"`
}

var defaults = options{
	AllowedPrefixes: "[]",
	CodeLength:      6,
	CodeTTL:         5 * time.Minute,
	CodeAttempts:    5,
	ResendInterval:  30 * time.Second,
	PerPhoneHourly:  10,
	PerPhoneDaily:   25,
	PerSourceHourly: 300,
	PerSourceDaily:  1000,

	PerSourceDistinctHourly: 20,
	PerSourceDistinctDaily:  60,
	PerSourceAttemptHourly:  60,

	GlobalLogin:         500,
	GlobalMFA:           300,
	GlobalEnroll:        100,
	GlobalReset:         200,
	GlobalAttemptLogin:  750,
	GlobalAttemptMFA:    450,
	GlobalAttemptEnroll: 150,
	GlobalAttemptReset:  300,

	PerFlowSends:     5,
	PerAccountHourly: 20,
}

func codeLengthInDomain(n int) bool { return n >= minCodeLength && n <= maxCodeLength }

func codeTTLInDomain(d time.Duration) bool {
	return d >= minCodeTTL && d <= maxCodeTTL
}
func codeAttemptsInDomain(n int) bool { return n >= minCodeAttempts && n <= maxCodeAttempts }
func resendIntervalInDomain(d time.Duration) bool {
	return d >= minResendInterval && d <= maxResendInterval
}
func perPhoneHourlyInDomain(n int) bool { return n >= minPerPhoneHourly && n <= maxPerPhoneHourly }
func perPhoneDailyInDomain(n int) bool  { return n >= minPerPhoneDaily && n <= maxPerPhoneDaily }
func perSourceHourlyInDomain(n int) bool {
	return n >= minPerSourceHourly && n <= maxPerSourceHourly
}

func perSourceDailyInDomain(n int) bool {
	return n >= minPerSourceDaily && n <= maxPerSourceDaily
}

func perSourceDistinctHourlyInDomain(n int) bool {
	return n >= minPerSourceDistinctHourly && n <= maxPerSourceDistinctHourly
}

func perSourceDistinctDailyInDomain(n int) bool {
	return n >= minPerSourceDistinctDaily && n <= maxPerSourceDistinctDaily
}

func perSourceAttemptHourlyInDomain(n int) bool {
	return n >= minPerSourceAttemptHourly && n <= maxPerSourceAttemptHourly
}

func globalHourlyInDomain(n int) bool { return n >= minGlobalHourly && n <= maxGlobalHourly }
func perFlowSendsInDomain(n int) bool { return n >= minPerFlowSends && n <= maxPerFlowSends }
func perAccountHourlyInDomain(n int) bool {
	return n >= minPerAccountHourly && n <= maxPerAccountHourly
}

func ceilingHourlyInDomain(n int) bool {
	return n >= budget.MinCeilingHourly && n <= budget.MaxCeilingHourly
}

func allowedPrefixesTextInDomain(text string) bool {
	_, err := parsePrefixes(text)
	return err == nil
}

func parsePrefixes(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var out []string
	dec := json.NewDecoder(strings.NewReader(text))
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			allowedPrefixesKey: {
				Description: "the E.164 prefixes a code may be sent to, as a JSON array of strings; " +
					"empty admits no number",
				Domain: settings.DomainOf("a JSON array of E.164 prefixes", allowedPrefixesTextInDomain),
			},
			codeLengthKey: {
				Description: "how many decimal digits a verification code has",
				Domain:      settings.DomainOf("whole numbers from 6 to 8", codeLengthInDomain),
			},
			codeTTLKey: {
				Description: "how long a code stays valid",
				Domain:      settings.DomainOf("durations from 1m0s to 10m0s", codeTTLInDomain),
			},
			codeAttemptsKey: {
				Description: "the most comparisons one code may take, after which the row is spent",
				Domain:      settings.DomainOf("whole numbers from 1 to 10", codeAttemptsInDomain),
			},
			resendIntervalKey: {
				Description: "how long after one send to the same number the next send has to wait",
				Domain:      settings.DomainOf("durations from 30s to 10m0s", resendIntervalInDomain),
			},
			perPhoneHourlyKey: {
				Description: "the most sends to one number in an hour",
				Domain:      settings.DomainOf("whole numbers from 1 to 60", perPhoneHourlyInDomain),
			},
			perPhoneDailyKey: {
				Description: "the most sends to one number in a day",
				Domain:      settings.DomainOf("whole numbers from 1 to 200", perPhoneDailyInDomain),
			},
			perSourceHourlyKey: {
				Description: "the most sends from one source address in an hour; a send with no " +
					"source address is not counted, because no trustworthy origin is known",
				Domain: settings.DomainOf("whole numbers from 1 to 10000", perSourceHourlyInDomain),
			},
			perSourceDailyKey: {
				Description: "the most sends from one source address in a day; a send with no " +
					"source address is not counted, because no trustworthy origin is known",
				Domain: settings.DomainOf("whole numbers from 1 to 100000", perSourceDailyInDomain),
			},
			perSourceDistinctHourlyKey: {
				Description: "the most different numbers one source address may send to in an hour; " +
					"a send with no source address is not counted, because no trustworthy origin is known",
				Domain: settings.DomainOf("whole numbers from 1 to 10000", perSourceDistinctHourlyInDomain),
			},
			perSourceDistinctDailyKey: {
				Description: "the most different numbers one source address may send to in a day; " +
					"a send with no source address is not counted, because no trustworthy origin is known",
				Domain: settings.DomainOf("whole numbers from 1 to 100000", perSourceDistinctDailyInDomain),
			},
			perSourceAttemptHourlyKey: {
				Description: "the most failed sends from one source address in an hour, counted only when " +
					"the send was refunded after a definitive failure; a send with no source address is " +
					"not counted, because no trustworthy origin is known",
				Domain: settings.DomainOf("whole numbers from 1 to 100000", perSourceAttemptHourlyInDomain),
			},
			globalLoginKey: {
				Description: "the most login codes sent in an hour, across every number",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalMFAKey: {
				Description: "the most multi-factor codes sent in an hour, across every number",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalEnrollKey: {
				Description: "the most enrollment codes sent in an hour, across every number",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalResetKey: {
				Description: "the most password reset codes sent in an hour, across every number",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalAttemptLoginKey: {
				Description: "the most failed login sends in an hour, across every number, counted only " +
					"when the send was refunded after a definitive failure; suppressed rows are not counted",
				Domain: settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalAttemptMFAKey: {
				Description: "the most failed multi-factor sends in an hour, across every number, " +
					"counted only when the send was refunded after a definitive failure; suppressed rows " +
					"are not counted",
				Domain: settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalAttemptEnrollKey: {
				Description: "the most failed enrollment sends in an hour, across every number, counted " +
					"only when the send was refunded after a definitive failure; suppressed rows are not counted",
				Domain: settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			globalAttemptResetKey: {
				Description: "the most failed password reset sends in an hour, across every number, " +
					"counted only when the send was refunded after a definitive failure; suppressed rows " +
					"are not counted",
				Domain: settings.DomainOf("whole numbers from 1 to 1000000", globalHourlyInDomain),
			},
			perFlowSendsKey: {
				Description: "the most sends one login flow may make; declared here and checked by the " +
					"caller, which holds the flow record",
				Domain: settings.DomainOf("whole numbers from 1 to 10", perFlowSendsInDomain),
			},
			perAccountHourlyKey: {
				Description: "the most mfa and enroll codes sent for one account in an hour; the login " +
					"purpose, which has no account, is not counted by it",
				Domain: settings.DomainOf("whole numbers from 1 to 60", perAccountHourlyInDomain),
			},
		},
	}
}

type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

func (r *reader) allowedPrefixes() []string {
	if r.store == nil {
		prefixes, _ := parsePrefixes(defaults.AllowedPrefixes)
		return prefixes
	}
	var text string
	if err := r.store.Get(settings.Global(), allowedPrefixesPath, &text); err != nil {
		r.report(allowedPrefixesPath, "the allowed table cannot be read, so it reads as empty and admits no number", err.Error())
		return nil
	}
	prefixes, err := parsePrefixes(text)
	if err != nil {
		r.report(allowedPrefixesPath, "the allowed table is not a JSON array of strings, so it reads as empty and admits no number", err.Error())
		return nil
	}
	r.report(allowedPrefixesPath, "", "")
	return prefixes
}

func (r *reader) codeLength() int {
	return readTuned(r, codeLengthPath, defaults.CodeLength, codeLengthInDomain)
}

func (r *reader) codeTTL() time.Duration {
	return readTuned(r, codeTTLPath, defaults.CodeTTL, codeTTLInDomain)
}

func (r *reader) codeAttempts() int {
	return readTuned(r, codeAttemptsPath, defaults.CodeAttempts, codeAttemptsInDomain)
}

func (r *reader) resendInterval() time.Duration {
	return readTuned(r, resendIntervalPath, defaults.ResendInterval, resendIntervalInDomain)
}

func (r *reader) perPhoneHourly() int {
	return readTuned(r, perPhoneHourlyPath, defaults.PerPhoneHourly, perPhoneHourlyInDomain)
}

func (r *reader) perPhoneDaily() int {
	return readTuned(r, perPhoneDailyPath, defaults.PerPhoneDaily, perPhoneDailyInDomain)
}

func (r *reader) perSourceHourly() int {
	return readTuned(r, perSourceHourlyPath, defaults.PerSourceHourly, perSourceHourlyInDomain)
}

func (r *reader) perSourceDaily() int {
	return readTuned(r, perSourceDailyPath, defaults.PerSourceDaily, perSourceDailyInDomain)
}

func (r *reader) perSourceDistinctHourly() int {
	return readTuned(r, perSourceDistinctHourlyPath, defaults.PerSourceDistinctHourly, perSourceDistinctHourlyInDomain)
}

func (r *reader) perSourceDistinctDaily() int {
	return readTuned(r, perSourceDistinctDailyPath, defaults.PerSourceDistinctDaily, perSourceDistinctDailyInDomain)
}

func (r *reader) perSourceAttemptHourly() int {
	return readTuned(r, perSourceAttemptHourlyPath, defaults.PerSourceAttemptHourly, perSourceAttemptHourlyInDomain)
}

func (r *reader) perAccountHourly() int {
	return readTuned(r, perAccountHourlyPath, defaults.PerAccountHourly, perAccountHourlyInDomain)
}

func (r *reader) ceilingHourly() int {
	return readTuned(r, ceilingHourlyPath, budget.DefaultCeilingHourly, ceilingHourlyInDomain)
}

func (r *reader) globalHourly(purpose string) int {
	switch purpose {
	case authn.SMSPurposeLogin:
		return readTuned(r, globalLoginPath, defaults.GlobalLogin, globalHourlyInDomain)
	case authn.SMSPurposeMFA:
		return readTuned(r, globalMFAPath, defaults.GlobalMFA, globalHourlyInDomain)
	case authn.SMSPurposeEnroll:
		return readTuned(r, globalEnrollPath, defaults.GlobalEnroll, globalHourlyInDomain)
	case authn.SMSPurposeReset:
		return readTuned(r, globalResetPath, defaults.GlobalReset, globalHourlyInDomain)
	}
	return defaults.GlobalLogin
}

func (r *reader) globalAttemptHourly(purpose string) int {
	switch purpose {
	case authn.SMSPurposeLogin:
		return readTuned(r, globalAttemptLoginPath, defaults.GlobalAttemptLogin, globalHourlyInDomain)
	case authn.SMSPurposeMFA:
		return readTuned(r, globalAttemptMFAPath, defaults.GlobalAttemptMFA, globalHourlyInDomain)
	case authn.SMSPurposeEnroll:
		return readTuned(r, globalAttemptEnrollPath, defaults.GlobalAttemptEnroll, globalHourlyInDomain)
	case authn.SMSPurposeReset:
		return readTuned(r, globalAttemptResetPath, defaults.GlobalAttemptReset, globalHourlyInDomain)
	}
	return defaults.GlobalAttemptLogin
}

func readTuned[T any](r *reader, path string, fallback T, inDomain func(T) bool) T {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[T](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(v):
		r.report(path, "the setting holds a value outside its domain, so its declared default is used", fmt.Sprint(v))
		return fallback
	}
	r.report(path, "", "")
	return v
}

func (r *reader) report(path, problem, detail string) {
	r.mu.Lock()
	last, reported := r.reported[path]
	switch {
	case problem == "":
		delete(r.reported, path)
	case !reported || last != detail:
		r.reported[path] = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}
