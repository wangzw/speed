package delivery

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms/budget"
)

const (
	perRecipientHourlyKey   = "per-recipient-hourly"
	perRecipientDailyKey    = "per-recipient-daily"
	globalHourlyKey         = "global-hourly"
	tenantDistinctHourlyKey = "tenant-distinct-hourly"
	attemptHourlyKey        = "attempt-hourly"
)

var (
	perRecipientHourlyPath   = ModuleName + "." + perRecipientHourlyKey
	perRecipientDailyPath    = ModuleName + "." + perRecipientDailyKey
	globalHourlyPath         = ModuleName + "." + globalHourlyKey
	tenantDistinctHourlyPath = ModuleName + "." + tenantDistinctHourlyKey
	attemptHourlyPath        = ModuleName + "." + attemptHourlyKey
)

// ceilingHourlyPath is the cross-channel ceiling the ledger module
// declares under the shared sms namespace.
const ceilingHourlyPath = "sms.ceiling-hourly"

func ceilingHourlyInDomain(n int) bool {
	return n >= budget.MinCeilingHourly && n <= budget.MaxCeilingHourly
}

const (
	minPerRecipientHourly = 1
	maxPerRecipientHourly = 1000

	minPerRecipientDaily = 1
	maxPerRecipientDaily = 10000

	minGlobalHourly = 1
	maxGlobalHourly = 10000000

	minTenantDistinctHourly = 1
	maxTenantDistinctHourly = 10000

	minAttemptHourly = 1
	maxAttemptHourly = 100000
)

type settingDefaults struct {
	PerRecipientHourly   int `config:"per-recipient-hourly"`
	PerRecipientDaily    int `config:"per-recipient-daily"`
	GlobalHourly         int `config:"global-hourly"`
	TenantDistinctHourly int `config:"tenant-distinct-hourly"`
	AttemptHourly        int `config:"attempt-hourly"`
}

var defaultSettings = settingDefaults{
	PerRecipientHourly:   20,
	PerRecipientDaily:    100,
	GlobalHourly:         10000,
	TenantDistinctHourly: 20,
	AttemptHourly:        300,
}

func perRecipientHourlyInDomain(n int) bool {
	return n >= minPerRecipientHourly && n <= maxPerRecipientHourly
}

func perRecipientDailyInDomain(n int) bool {
	return n >= minPerRecipientDaily && n <= maxPerRecipientDaily
}

func globalHourlyInDomain(n int) bool { return n >= minGlobalHourly && n <= maxGlobalHourly }

func tenantDistinctHourlyInDomain(n int) bool {
	return n >= minTenantDistinctHourly && n <= maxTenantDistinctHourly
}

func attemptHourlyInDomain(n int) bool { return n >= minAttemptHourly && n <= maxAttemptHourly }

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaultSettings}},
		Items: map[string]settings.Item{
			perRecipientHourlyKey: {
				Description: "the most email or sms messages one recipient may be sent in an hour, counted per channel",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000", perRecipientHourlyInDomain),
			},
			perRecipientDailyKey: {
				Description: "the most email or sms messages one recipient may be sent in a day, counted per channel",
				Domain:      settings.DomainOf("whole numbers from 1 to 10000", perRecipientDailyInDomain),
			},
			globalHourlyKey: {
				Description: "the most email messages the whole deployment may send in an hour, " +
					"across every recipient and tenant",
				Domain: settings.DomainOf("whole numbers from 1 to 10000000", globalHourlyInDomain),
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

// budgetLimits is the limits one dispatch is checked against, read once
// per dispatch so that a change reaches the next dispatch without a
// restart.
type budgetLimits struct {
	ceilingHourly        int
	perRecipientHourly   int
	perRecipientDaily    int
	globalHourly         int
	tenantDistinctHourly int
	attemptHourly        int
}

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

func (r *settingReader) limits() budgetLimits {
	return budgetLimits{
		ceilingHourly:        readTuned(r, ceilingHourlyPath, budget.DefaultCeilingHourly, ceilingHourlyInDomain),
		perRecipientHourly:   readTuned(r, perRecipientHourlyPath, defaultSettings.PerRecipientHourly, perRecipientHourlyInDomain),
		perRecipientDaily:    readTuned(r, perRecipientDailyPath, defaultSettings.PerRecipientDaily, perRecipientDailyInDomain),
		globalHourly:         readTuned(r, globalHourlyPath, defaultSettings.GlobalHourly, globalHourlyInDomain),
		tenantDistinctHourly: readTuned(r, tenantDistinctHourlyPath, defaultSettings.TenantDistinctHourly, tenantDistinctHourlyInDomain),
		attemptHourly:        readTuned(r, attemptHourlyPath, defaultSettings.AttemptHourly, attemptHourlyInDomain),
	}
}

// readTuned reads a budget setting: one that fails to read, and one
// outside its domain, read as the declared default, and the fault is
// reported at Error once per distinct fault.
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
