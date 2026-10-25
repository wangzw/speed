package delivery

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/vislake/speed/pkg/settings"
)

// The setting keys this module declares, relative to its namespace, and the full
// paths a read goes by. The three bound an external delivery: two windows per
// recipient and one window across the deployment.
const (
	perRecipientHourlyKey = "per-recipient-hourly"
	perRecipientDailyKey  = "per-recipient-daily"
	globalHourlyKey       = "global-hourly"
)

// The full paths the reads go by.
var (
	perRecipientHourlyPath = ModuleName + "." + perRecipientHourlyKey
	perRecipientDailyPath  = ModuleName + "." + perRecipientDailyKey
	globalHourlyPath       = ModuleName + "." + globalHourlyKey
)

// The bounds of the setting domains. The floor is one and not zero: a budget is
// a security bound, and a zero would turn it off. An operator who wants a limit
// effectively off states a limit that traffic will not reach rather than
// removing the bound.
const (
	minPerRecipientHourly = 1
	maxPerRecipientHourly = 1000

	minPerRecipientDaily = 1
	maxPerRecipientDaily = 10000

	minGlobalHourly = 1
	maxGlobalHourly = 10000000
)

// settingDefaults carries the declared default of each setting. The values are
// this module's own: twenty external messages an hour and one hundred a day to
// one recipient leave room for ordinary business notification while stopping a
// producer that has begun to loop, and the deployment-wide ten thousand an hour
// bounds the carrier bill a runaway producer can run up. An operator tunes them
// from the settings surface without a restart.
type settingDefaults struct {
	PerRecipientHourly int `config:"per-recipient-hourly"`
	PerRecipientDaily  int `config:"per-recipient-daily"`
	GlobalHourly       int `config:"global-hourly"`
}

// defaultSettings is what this module is worth without a settings row.
var defaultSettings = settingDefaults{
	PerRecipientHourly: 20,
	PerRecipientDaily:  100,
	GlobalHourly:       10000,
}

// The domains of the settings.
func perRecipientHourlyInDomain(n int) bool {
	return n >= minPerRecipientHourly && n <= maxPerRecipientHourly
}

func perRecipientDailyInDomain(n int) bool {
	return n >= minPerRecipientDaily && n <= maxPerRecipientDaily
}

func globalHourlyInDomain(n int) bool { return n >= minGlobalHourly && n <= maxGlobalHourly }

// settingsSchema declares this module's delivery budgets. The namespace is the
// module's own name, as it is for every registering module, so an operator
// writes the full path notification.delivery.per-recipient-hourly.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaultSettings}},
		Items: map[string]settings.Item{
			perRecipientHourlyKey: {
				Description: "the most external messages one recipient may be sent in an hour",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000", perRecipientHourlyInDomain),
			},
			perRecipientDailyKey: {
				Description: "the most external messages one recipient may be sent in a day",
				Domain:      settings.DomainOf("whole numbers from 1 to 10000", perRecipientDailyInDomain),
			},
			globalHourlyKey: {
				Description: "the most external messages the whole deployment may send in an hour, " +
					"across every recipient and tenant",
				Domain: settings.DomainOf("whole numbers from 1 to 10000000", globalHourlyInDomain),
			},
		},
	}
}

// budgetLimits is the three limits one dispatch is checked against, read once
// per dispatch so that a change reaches the next dispatch without a restart.
type budgetLimits struct {
	perRecipientHourly int
	perRecipientDaily  int
	globalHourly       int
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
		perRecipientHourly: readTuned(r, perRecipientHourlyPath, defaultSettings.PerRecipientHourly, perRecipientHourlyInDomain),
		perRecipientDaily:  readTuned(r, perRecipientDailyPath, defaultSettings.PerRecipientDaily, perRecipientDailyInDomain),
		globalHourly:       readTuned(r, globalHourlyPath, defaultSettings.GlobalHourly, globalHourlyInDomain),
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
