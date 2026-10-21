package db

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The runtime tunables this module declares as settings, relative to its
// namespace, and the full paths a read goes by.
const (
	pollIntervalItem = "poll-interval"
	claimBatchItem   = "claim-batch"
	leaseItem        = "lease"
	retryBaseItem    = "retry-base"
	retryMaxItem     = "retry-max"

	pollIntervalPath = ConfigNamespace + "." + pollIntervalItem
	claimBatchPath   = ConfigNamespace + "." + claimBatchItem
	leasePath        = ConfigNamespace + "." + leaseItem
	retryBasePath    = ConfigNamespace + "." + retryBaseItem
	retryMaxPath     = ConfigNamespace + "." + retryMaxItem
)

// runtimeTunables carries the runtime settings and their declared defaults. The
// settings module reads the defaults off it and it never receives a value.
type runtimeTunables struct {
	PollInterval time.Duration `config:"poll-interval"`
	ClaimBatch   int           `config:"claim-batch"`
	Lease        time.Duration `config:"lease"`
	RetryBase    time.Duration `config:"retry-base"`
	RetryMax     time.Duration `config:"retry-max"`
}

// tunableDefaults is what the queue runs on when neither the store nor a written
// row says otherwise: the values the design fixes.
var tunableDefaults = runtimeTunables{
	PollInterval: time.Second,
	ClaimBatch:   16,
	Lease:        2 * time.Minute,
	RetryBase:    time.Second,
	RetryMax:     time.Minute,
}

// The domains of the settings. Each is the one statement of its range: the
// settings store holds a write to it, and a read is judged against it again,
// since a row stored before the domain was declared, or written around the
// store, is served as it is. The two retry bounds are judged one at a time;
// their order is settled where a retry is delayed, not as a startup invariant.
//
// The lease, the retry base and the retry cap are stored in whole milliseconds,
// so each admits no value below one millisecond: a shorter one truncates to zero
// when the queue converts it, which would expire a claim at once or retry a job
// immediately. The poll interval is not converted, so any positive duration is a
// meaningful wait.
func pollIntervalInDomain(d time.Duration) bool { return d > 0 }

func claimBatchInDomain(n int) bool { return n > 0 }

func leaseInDomain(d time.Duration) bool { return d >= time.Millisecond }

func retryBaseInDomain(d time.Duration) bool { return d >= time.Millisecond }

func retryMaxInDomain(d time.Duration) bool { return d >= time.Millisecond }

// settingsSchema declares this module's runtime tunables, each with the domain
// the loop acts on.
//
// Every one of them is read while the queue runs: the poll interval per loop
// iteration, the claim batch and the lease per claim, and the retry bounds per
// failed settlement. The worker count and the shutdown grace are not here: both
// size the construction, so a change to either needs a restart.
//
// The items hang from the module's namespace as a mount rather than as the
// schema's namespace, which keeps each item keyed by the full path a write
// names: jobs.db.poll-interval and the four beside it.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Mounts: []settings.Mount{{Path: ConfigNamespace, Value: &tunableDefaults}},
		Items: map[string]settings.Item{
			pollIntervalPath: {
				Description: "how often an executor reads the job table when it found no due job, " +
					"which must be positive",
				Domain: settings.DomainOf("the positive durations", pollIntervalInDomain),
			},
			claimBatchPath: {
				Description: "how many due jobs one claim takes, which must be positive",
				Domain:      settings.DomainOf("the positive integers", claimBatchInDomain),
			},
			leasePath: {
				Description: "how long one claim holds the jobs it took, and so how long a job held " +
					"by a replica that died waits before another replica takes it over; it must be " +
					"at least one millisecond",
				Domain: settings.DomainOf("the durations of at least one millisecond", leaseInDomain),
			},
			retryBasePath: {
				Description: "the delay after a job's first failure, doubled with each further " +
					"failure; it must be at least one millisecond",
				Domain: settings.DomainOf("the durations of at least one millisecond", retryBaseInDomain),
			},
			retryMaxPath: {
				Description: "the cap on that doubling, which must be at least one millisecond; a " +
					"cap below the base delays the next retry by the cap",
				Domain: settings.DomainOf("the durations of at least one millisecond", retryMaxInDomain),
			},
		},
	}
}

// settingsReader reads the runtime tunables. The store is nil when the assembly
// has none, and every read then answers its declared default, which is a legal
// assembly and says nothing.
type settingsReader struct {
	store settings.Store

	// mu guards reported, which holds for each setting the fault last written, so
	// that a setting read on every round is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

func newSettingsReader(store settings.Store) *settingsReader {
	return &settingsReader{store: store, reported: make(map[string]string)}
}

// poll is how often an executor reads the table when it found no due job.
func (r *settingsReader) poll(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, pollIntervalPath, tunableDefaults.PollInterval, pollIntervalInDomain)
}

// batch is how many due jobs one claim takes.
func (r *settingsReader) batch(logger *slog.Logger) int {
	return readTuned(r, logger, claimBatchPath, tunableDefaults.ClaimBatch, claimBatchInDomain)
}

// lease is how long one claim holds the rows it took.
func (r *settingsReader) lease(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, leasePath, tunableDefaults.Lease, leaseInDomain)
}

// retryBase is the delay after a job's first failure.
func (r *settingsReader) retryBase(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, retryBasePath, tunableDefaults.RetryBase, retryBaseInDomain)
}

// retryMax caps the doubling of that delay.
func (r *settingsReader) retryMax(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, retryMaxPath, tunableDefaults.RetryMax, retryMaxInDomain)
}

// readTuned reads one tuning setting: one that fails to read, and one outside
// its domain, answers the declared default, and the fault is reported at Error.
func readTuned[T any](r *settingsReader, logger *slog.Logger, path string, fallback T, inDomain func(T) bool) T {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[T](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(logger, path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(v):
		r.report(logger, path, "the setting holds a value outside its domain, so its declared default is used",
			fmt.Sprint(v))
		return fallback
	}
	r.report(logger, path, "", "")
	return v
}

// report writes the Error line of a setting read in a way other than as it says.
// The same fault is written once and again only when it changes or has been
// cleared in between; an empty problem clears it.
func (r *settingsReader) report(logger *slog.Logger, path, problem, detail string) {
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
		logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}

// retryAfter is the delay after a job's attempts-th failure: the base, doubled
// once per failure before it and capped at the maximum. A cap below the base
// delays the retry by the cap.
func retryAfter(base, ceiling time.Duration, attempts int) time.Duration {
	delay := base
	for i := 1; i < attempts && delay < ceiling; i++ {
		delay *= 2
	}
	return min(delay, ceiling)
}
