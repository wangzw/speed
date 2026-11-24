package db

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

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

type runtimeTunables struct {
	PollInterval time.Duration `config:"poll-interval"`
	ClaimBatch   int           `config:"claim-batch"`
	Lease        time.Duration `config:"lease"`
	RetryBase    time.Duration `config:"retry-base"`
	RetryMax     time.Duration `config:"retry-max"`
}

var tunableDefaults = runtimeTunables{
	PollInterval: time.Second,
	ClaimBatch:   16,
	Lease:        2 * time.Minute,
	RetryBase:    time.Second,
	RetryMax:     time.Minute,
}

func pollIntervalInDomain(d time.Duration) bool { return d > 0 }

func claimBatchInDomain(n int) bool { return n > 0 }

func leaseInDomain(d time.Duration) bool { return d >= time.Millisecond }

func retryBaseInDomain(d time.Duration) bool { return d >= time.Millisecond }

func retryMaxInDomain(d time.Duration) bool { return d >= time.Millisecond }

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

type settingsReader struct {
	store settings.Store

	mu       sync.Mutex
	reported map[string]string
}

func newSettingsReader(store settings.Store) *settingsReader {
	return &settingsReader{store: store, reported: make(map[string]string)}
}

func (r *settingsReader) poll(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, pollIntervalPath, tunableDefaults.PollInterval, pollIntervalInDomain)
}

func (r *settingsReader) batch(logger *slog.Logger) int {
	return readTuned(r, logger, claimBatchPath, tunableDefaults.ClaimBatch, claimBatchInDomain)
}

func (r *settingsReader) lease(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, leasePath, tunableDefaults.Lease, leaseInDomain)
}

func (r *settingsReader) retryBase(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, retryBasePath, tunableDefaults.RetryBase, retryBaseInDomain)
}

func (r *settingsReader) retryMax(logger *slog.Logger) time.Duration {
	return readTuned(r, logger, retryMaxPath, tunableDefaults.RetryMax, retryMaxInDomain)
}

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

func retryAfter(base, ceiling time.Duration, attempts int) time.Duration {
	delay := base
	for i := 1; i < attempts && delay < ceiling; i++ {
		delay *= 2
	}
	return min(delay, ceiling)
}
