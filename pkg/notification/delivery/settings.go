package delivery

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/vislake/speed/pkg/settings"
)

// The setting keys this module declares, relative to its namespace, and the full
// paths a read goes by. The four bound an sms delivery: two windows per recipient
// and two fan-out windows per tenant.
const (
	perRecipientHourlyKey   = "per-recipient-hourly"
	perRecipientDailyKey    = "per-recipient-daily"
	tenantDistinctHourlyKey = "tenant-distinct-hourly"
	attemptHourlyKey        = "attempt-hourly"
)

// The full paths the reads go by.
var (
	perRecipientHourlyPath   = ModuleName + "." + perRecipientHourlyKey
	perRecipientDailyPath    = ModuleName + "." + perRecipientDailyKey
	tenantDistinctHourlyPath = ModuleName + "." + tenantDistinctHourlyKey
	attemptHourlyPath        = ModuleName + "." + attemptHourlyKey
)

// ceilingHourlyPath is the cross-channel ceiling the ledger module declares
// under the shared sms namespace. This module reads it and declares no copy of
// its own: the count spans both sending channels, so a second, notification-only
// ceiling would only add a second bill.
const ceilingHourlyPath = "sms.ceiling-hourly"

// The ceiling's domain and declared default. They are the ledger module's, read
// here so a deployment that has not tuned the ceiling reads the same number
// whichever channel consults it.
const (
	minCeilingHourly     = 1
	maxCeilingHourly     = 1000000
	defaultCeilingHourly = 5000
)

func ceilingHourlyInDomain(n int) bool { return n >= minCeilingHourly && n <= maxCeilingHourly }

// The bounds of the setting domains. The floor is one and not zero: a budget is
// a security bound, and a zero would turn it off. An operator who wants a limit
// effectively off states a limit that traffic will not reach rather than
// removing the bound.
const (
	minPerRecipientHourly = 1
	maxPerRecipientHourly = 1000

	minPerRecipientDaily = 1
	maxPerRecipientDaily = 10000

	minTenantDistinctHourly = 1
	maxTenantDistinctHourly = 10000

	minAttemptHourly = 1
	maxAttemptHourly = 100000
)

// settingDefaults carries the declared default of each setting. The values are
// this module's own: twenty external messages an hour and one hundred a day to
// one recipient leave room for ordinary business notification while stopping a
// producer that has begun to loop. Twenty different recipients an hour bound one
// tenant's fan-out, and three hundred attempts an hour bound a producer that
// keeps aiming at destinations whose sends fail. An operator tunes them from the
// settings surface without a restart.
type settingDefaults struct {
	PerRecipientHourly   int `config:"per-recipient-hourly"`
	PerRecipientDaily    int `config:"per-recipient-daily"`
	TenantDistinctHourly int `config:"tenant-distinct-hourly"`
	AttemptHourly        int `config:"attempt-hourly"`
}

// defaultSettings is what this module is worth without a settings row.
var defaultSettings = settingDefaults{
	PerRecipientHourly:   20,
	PerRecipientDaily:    100,
	TenantDistinctHourly: 20,
	AttemptHourly:        300,
}

// The domains of the settings.
func perRecipientHourlyInDomain(n int) bool {
	return n >= minPerRecipientHourly && n <= maxPerRecipientHourly
}

func perRecipientDailyInDomain(n int) bool {
	return n >= minPerRecipientDaily && n <= maxPerRecipientDaily
}

func tenantDistinctHourlyInDomain(n int) bool {
	return n >= minTenantDistinctHourly && n <= maxTenantDistinctHourly
}

func attemptHourlyInDomain(n int) bool { return n >= minAttemptHourly && n <= maxAttemptHourly }

// settingsSchema declares this module's delivery budgets. The namespace is the
// module's own name, as it is for every registering module, so an operator
// writes the full path notification.delivery.per-recipient-hourly.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaultSettings}},
		Items: map[string]settings.Item{
			perRecipientHourlyKey: {
				Description: "the most sms messages one recipient may be sent in an hour",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000", perRecipientHourlyInDomain),
			},
			perRecipientDailyKey: {
				Description: "the most sms messages one recipient may be sent in a day",
				Domain:      settings.DomainOf("whole numbers from 1 to 10000", perRecipientDailyInDomain),
			},
			tenantDistinctHourlyKey: {
				Description: "the most different recipients one tenant may be sent an sms message in an hour",
				Domain:      settings.DomainOf("whole numbers from 1 to 10000", tenantDistinctHourlyInDomain),
			},
			attemptHourlyKey: {
				Description: "the most sms send attempts one tenant may make in an hour, counted whether " +
					"or not the carrier accepted the message",
				Domain: settings.DomainOf("whole numbers from 1 to 100000", attemptHourlyInDomain),
			},
		},
	}
}

// budgetLimits is the limits one dispatch is checked against, read once per
// dispatch so that a change reaches the next dispatch without a restart. The
// ceiling is the one count both sending channels share; the other four are this
// channel's own.
type budgetLimits struct {
	ceilingHourly        int
	perRecipientHourly   int
	perRecipientDaily    int
	tenantDistinctHourly int
	attemptHourly        int
}

// settingReader reads the delivery budgets. The store is nil when the assembly
// has none, and every setting then reads as its declared default, which is a
// legal assembly.
type settingReader struct {
	store  settings.Store
	logger *slog.Logger

	// mu guards reported, which holds for each setting the fault last written, so
	// that a setting read on every dispatch is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

func newSettingReader(store settings.Store, logger *slog.Logger) *settingReader {
	if logger == nil {
		logger = slog.Default()
	}
	return &settingReader{store: store, logger: logger, reported: make(map[string]string)}
}

// limits reads every budget the dispatch check needs.
func (r *settingReader) limits() budgetLimits {
	return budgetLimits{
		ceilingHourly:        readTuned(r, ceilingHourlyPath, defaultCeilingHourly, ceilingHourlyInDomain),
		perRecipientHourly:   readTuned(r, perRecipientHourlyPath, defaultSettings.PerRecipientHourly, perRecipientHourlyInDomain),
		perRecipientDaily:    readTuned(r, perRecipientDailyPath, defaultSettings.PerRecipientDaily, perRecipientDailyInDomain),
		tenantDistinctHourly: readTuned(r, tenantDistinctHourlyPath, defaultSettings.TenantDistinctHourly, tenantDistinctHourlyInDomain),
		attemptHourly:        readTuned(r, attemptHourlyPath, defaultSettings.AttemptHourly, attemptHourlyInDomain),
	}
}

// readTuned reads a budget setting: one that fails to read, and one outside its
// domain, read as the declared default, and the fault is reported at Error once
// per distinct fault. A failed read therefore falls back to a bound, never to no
// bound.
func readTuned[T any](r *settingReader, path string, fallback T, inDomain func(T) bool) T {
	if r == nil || r.store == nil {
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
// it says. A setting is read on dispatches, so the same fault is written once and
// again only when it changes or has been cleared in between; an empty problem
// clears it.
func (r *settingReader) report(path, problem, detail string) {
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
