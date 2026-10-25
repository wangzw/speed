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
)

// The setting keys this module declares, relative to its namespace, and the
// full paths a read goes by.
const (
	allowedPrefixesKey  = "allowed-prefixes"
	codeLengthKey       = "code-length"
	codeTTLKey          = "code-ttl"
	codeAttemptsKey     = "code-attempts"
	resendIntervalKey   = "resend-interval"
	perPhoneHourlyKey   = "per-phone-hourly"
	perPhoneDailyKey    = "per-phone-daily"
	perSourceHourlyKey  = "per-source-hourly"
	perSourceDailyKey   = "per-source-daily"
	globalLoginKey      = "global-hourly-login"
	globalMFAKey        = "global-hourly-mfa"
	globalEnrollKey     = "global-hourly-enroll"
	perFlowSendsKey     = "per-flow-sends"
	perAccountHourlyKey = "per-account-hourly"
)

// The full paths the reads and the relaxation watch go by.
var (
	allowedPrefixesPath  = ModuleName + "." + allowedPrefixesKey
	codeLengthPath       = ModuleName + "." + codeLengthKey
	codeTTLPath          = ModuleName + "." + codeTTLKey
	codeAttemptsPath     = ModuleName + "." + codeAttemptsKey
	resendIntervalPath   = ModuleName + "." + resendIntervalKey
	perPhoneHourlyPath   = ModuleName + "." + perPhoneHourlyKey
	perPhoneDailyPath    = ModuleName + "." + perPhoneDailyKey
	perSourceHourlyPath  = ModuleName + "." + perSourceHourlyKey
	perSourceDailyPath   = ModuleName + "." + perSourceDailyKey
	globalLoginPath      = ModuleName + "." + globalLoginKey
	globalMFAPath        = ModuleName + "." + globalMFAKey
	globalEnrollPath     = ModuleName + "." + globalEnrollKey
	perFlowSendsPath     = ModuleName + "." + perFlowSendsKey
	perAccountHourlyPath = ModuleName + "." + perAccountHourlyKey
)

// The bounds of the setting domains.
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

	minGlobalHourly = 1
	maxGlobalHourly = 1000000

	minPerFlowSends = 1
	maxPerFlowSends = 10

	minPerAccountHourly = 1
	maxPerAccountHourly = 60
)

// options carries the settings this module declares and their defaults, which
// the settings module reads off it and which it never receives a value for.
// AllowedPrefixes is a string item holding JSON, because a setting item is a
// scalar.
type options struct {
	AllowedPrefixes  string        `config:"allowed-prefixes"`
	CodeLength       int           `config:"code-length"`
	CodeTTL          time.Duration `config:"code-ttl"`
	CodeAttempts     int           `config:"code-attempts"`
	ResendInterval   time.Duration `config:"resend-interval"`
	PerPhoneHourly   int           `config:"per-phone-hourly"`
	PerPhoneDaily    int           `config:"per-phone-daily"`
	PerSourceHourly  int           `config:"per-source-hourly"`
	PerSourceDaily   int           `config:"per-source-daily"`
	GlobalLogin      int           `config:"global-hourly-login"`
	GlobalMFA        int           `config:"global-hourly-mfa"`
	GlobalEnroll     int           `config:"global-hourly-enroll"`
	PerFlowSends     int           `config:"per-flow-sends"`
	PerAccountHourly int           `config:"per-account-hourly"`
}

// defaults is the declared default of each setting. The default of
// AllowedPrefixes is the empty table, which admits no number: a deployment that
// does not name a region sends nothing.
var defaults = options{
	AllowedPrefixes:  "[]",
	CodeLength:       6,
	CodeTTL:          5 * time.Minute,
	CodeAttempts:     5,
	ResendInterval:   60 * time.Second,
	PerPhoneHourly:   5,
	PerPhoneDaily:    10,
	PerSourceHourly:  30,
	PerSourceDaily:   100,
	GlobalLogin:      300,
	GlobalMFA:        300,
	GlobalEnroll:     100,
	PerFlowSends:     3,
	PerAccountHourly: 10,
}

// The domains of the settings. Each is the one statement of its range.
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
func globalHourlyInDomain(n int) bool { return n >= minGlobalHourly && n <= maxGlobalHourly }
func perFlowSendsInDomain(n int) bool { return n >= minPerFlowSends && n <= maxPerFlowSends }
func perAccountHourlyInDomain(n int) bool {
	return n >= minPerAccountHourly && n <= maxPerAccountHourly
}

// allowedPrefixesTextInDomain reports whether a JSON array of strings is a legal
// value of the allowed-prefixes setting: the text parses as an array of strings.
func allowedPrefixesTextInDomain(text string) bool {
	_, err := parsePrefixes(text)
	return err == nil
}

// parsePrefixes reads a JSON array of strings from the text of the
// allowed-prefixes setting. The empty string reads as the empty list, which
// admits no number.
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

// settingsSchema declares this module's runtime parameters, each with the domain
// the code acts on.
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

// reader reads the settings with the failure direction each has. The store is
// nil when the assembly has none, and every setting then reads as its declared
// default, which is a legal assembly.
type reader struct {
	store  settings.Store
	logger *slog.Logger

	// mu guards reported, which holds for each setting the fault last written, so
	// that a setting read on every request is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

// allowedPrefixes reads the allowed table. A read that fails, and a value that
// is not a JSON array, read as the empty table, which admits no number: the
// default is the strict direction, so a failed read refuses every send.
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

func (r *reader) perAccountHourly() int {
	return readTuned(r, perAccountHourlyPath, defaults.PerAccountHourly, perAccountHourlyInDomain)
}

// globalHourly reads the global hourly budget of one purpose. The purpose is one
// of the closed set, which the caller has already held the input to.
func (r *reader) globalHourly(purpose string) int {
	switch purpose {
	case authn.SMSPurposeLogin:
		return readTuned(r, globalLoginPath, defaults.GlobalLogin, globalHourlyInDomain)
	case authn.SMSPurposeMFA:
		return readTuned(r, globalMFAPath, defaults.GlobalMFA, globalHourlyInDomain)
	case authn.SMSPurposeEnroll:
		return readTuned(r, globalEnrollPath, defaults.GlobalEnroll, globalHourlyInDomain)
	}
	return defaults.GlobalLogin
}

// readTuned reads a tuning setting: one that fails to read, and one outside its
// domain, read as the declared default, and the fault is reported at Error once
// per distinct fault.
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

// report writes the Error line of a setting that is read in a way other than as
// it says. A setting is read on requests, so the same fault is written once and
// again only when it changes or has been cleared in between; an empty problem
// clears it.
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
