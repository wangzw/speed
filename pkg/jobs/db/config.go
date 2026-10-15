package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

// ErrInvalidConfig reports a configuration this module cannot run on: a
// duration or a count outside the range the claim loop can work with. It is
// refused in Prepare, so a host that wrote the value reads why rather than
// meeting it as a queue that claims nothing.
var ErrInvalidConfig = errors.New("jobs.db: the configuration holds a value the queue cannot run on")

// The keys this module accepts, relative to its namespace.
const (
	pollIntervalItem  = "poll-interval"
	workersItem       = "workers"
	claimBatchItem    = "claim-batch"
	leaseItem         = "lease"
	shutdownGraceItem = "shutdown-grace"
	retryBaseItem     = "retry-base"
	retryMaxItem      = "retry-max"
)

// configDefaults is what this module is worth without a configuration file:
// the values the design fixes.
var configDefaults = moduleConfig{
	PollInterval:  time.Second,
	Workers:       4,
	ClaimBatch:    16,
	Lease:         2 * time.Minute,
	ShutdownGrace: 10 * time.Second,
	RetryBase:     time.Second,
	RetryMax:      time.Minute,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// PollInterval is how often an executor reads the table when it found no
	// due job, and the longest it sleeps before reading again when the next
	// job falls due further out.
	PollInterval time.Duration `config:"poll-interval"`
	// Workers is how many executions this process runs at once.
	Workers int `config:"workers"`
	// ClaimBatch is how many due jobs one claim takes.
	ClaimBatch int `config:"claim-batch"`
	// Lease is how long one claim holds the rows it took, and the earliest a
	// row a replica died holding is taken over.
	Lease time.Duration `config:"lease"`
	// ShutdownGrace is how long the stop stage waits for an execution under
	// way before cancelling it.
	ShutdownGrace time.Duration `config:"shutdown-grace"`
	// RetryBase is the delay after a job's first failure. It doubles with each
	// further failure up to RetryMax.
	RetryBase time.Duration `config:"retry-base"`
	// RetryMax caps that doubling.
	RetryMax time.Duration `config:"retry-max"`
}

// settings is the configuration this module runs on, resolved and checked.
type settings struct {
	poll      time.Duration
	workers   int
	batch     int
	lease     time.Duration
	grace     time.Duration
	retryBase time.Duration
	retryMax  time.Duration
}

// resolve turns the declared configuration into what the module runs on,
// refusing a value the claim loop cannot work with.
func (c moduleConfig) resolve() (settings, error) {
	switch {
	case c.PollInterval <= 0:
		return settings{}, fmt.Errorf("%w: %s.%s is %s, and an executor reads the table on a positive interval",
			ErrInvalidConfig, ConfigNamespace, pollIntervalItem, c.PollInterval)
	case c.Workers <= 0:
		return settings{}, fmt.Errorf("%w: %s.%s is %d, and a process runs at least one execution",
			ErrInvalidConfig, ConfigNamespace, workersItem, c.Workers)
	case c.ClaimBatch <= 0:
		return settings{}, fmt.Errorf("%w: %s.%s is %d, and a claim takes at least one job",
			ErrInvalidConfig, ConfigNamespace, claimBatchItem, c.ClaimBatch)
	case c.Lease <= 0:
		return settings{}, fmt.Errorf("%w: %s.%s is %s, and a claim has to outlast the instant it is taken",
			ErrInvalidConfig, ConfigNamespace, leaseItem, c.Lease)
	case c.ShutdownGrace < 0:
		return settings{}, fmt.Errorf("%w: %s.%s is %s, and the stop stage cannot wait a negative time",
			ErrInvalidConfig, ConfigNamespace, shutdownGraceItem, c.ShutdownGrace)
	case c.RetryBase <= 0:
		return settings{}, fmt.Errorf("%w: %s.%s is %s, and the first retry is delayed by a positive time",
			ErrInvalidConfig, ConfigNamespace, retryBaseItem, c.RetryBase)
	case c.RetryMax < c.RetryBase:
		return settings{}, fmt.Errorf("%w: %s.%s is %s and %s.%s is %s, and the cap cannot be below the base",
			ErrInvalidConfig, ConfigNamespace, retryMaxItem, c.RetryMax, ConfigNamespace, retryBaseItem, c.RetryBase)
	}
	return settings{
		poll:      c.PollInterval,
		workers:   c.Workers,
		batch:     c.ClaimBatch,
		lease:     c.Lease,
		grace:     c.ShutdownGrace,
		retryBase: c.RetryBase,
		retryMax:  c.RetryMax,
	}, nil
}

// retryAfter is the delay after a job's attempts-th failure: the base, doubled
// once per failure before it and capped at the maximum.
func (s settings) retryAfter(attempts int) time.Duration {
	delay := s.retryBase
	for i := 1; i < attempts && delay < s.retryMax; i++ {
		delay *= 2
	}
	return min(delay, s.retryMax)
}

// schema declares this module's input items.
//
// The namespace is this module's own name, as it is for every registering
// module: the list is collected over every registered module whether or not it
// runs, so two modules declaring one path is a startup failure even when only
// one of them ever runs.
//
// Every item takes its value from the primary config source alone. These are
// the parameters one deployment runs its queue by, and the source a host writes
// them into is the one the module reads them from; an environment variable or a
// command-line argument for one of them is not read, which the loader reports
// as a line nobody consumes.
func schema() config.Schema {
	item := func(description string) config.Item {
		return config.Item{Origins: config.OriginPrimary, Description: description}
	}
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			pollIntervalItem: item("how often an executor reads the job table when it found no due job"),
			workersItem:      item("how many jobs this process executes at once"),
			claimBatchItem:   item("how many due jobs one claim takes"),
			leaseItem: item("how long one claim holds the jobs it took, and so how long a job held by a " +
				"replica that died waits before another replica takes it over"),
			shutdownGraceItem: item("how long the stop stage waits for an execution under way before " +
				"cancelling it"),
			retryBaseItem: item("the delay after a job's first failure, doubled with each further failure"),
			retryMaxItem:  item("the cap on that doubling"),
		},
	}
}

// readSettings decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}
