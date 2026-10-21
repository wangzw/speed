package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

// ErrInvalidConfig reports a configuration this module cannot run on: a
// worker count or a shutdown grace outside the range the queue can work with.
// It is refused in Prepare, so a host that wrote the value reads why rather than
// meeting it as a queue that runs nothing.
var ErrInvalidConfig = errors.New("jobs.db: the configuration holds a value the queue cannot run on")

// The configuration items this module accepts, relative to its namespace. Both
// size the construction and are read once: the worker pool is built with the
// count and the stop stage waits the grace, so a change to either requires a
// restart.
const (
	workersItem       = "workers"
	shutdownGraceItem = "shutdown-grace"
)

// configDefaults is what this module is worth without a configuration file:
// the values the design fixes.
var configDefaults = moduleConfig{
	Workers:       4,
	ShutdownGrace: 10 * time.Second,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Workers is how many executions this process runs at once.
	Workers int `config:"workers"`
	// ShutdownGrace is how long the stop stage waits for an execution under
	// way before cancelling it.
	ShutdownGrace time.Duration `config:"shutdown-grace"`
}

// construction is the configuration this module runs on, resolved and checked.
type construction struct {
	workers int
	grace   time.Duration
}

// resolve turns the declared configuration into what the module is constructed
// with, refusing a value it cannot work with.
func (c moduleConfig) resolve() (construction, error) {
	switch {
	case c.Workers <= 0:
		return construction{}, fmt.Errorf("%w: %s.%s is %d, and a process runs at least one execution",
			ErrInvalidConfig, ConfigNamespace, workersItem, c.Workers)
	case c.ShutdownGrace < 0:
		return construction{}, fmt.Errorf("%w: %s.%s is %s, and the stop stage cannot wait a negative time",
			ErrInvalidConfig, ConfigNamespace, shutdownGraceItem, c.ShutdownGrace)
	}
	return construction{
		workers: c.Workers,
		grace:   c.ShutdownGrace,
	}, nil
}

// schema declares this module's input items.
//
// The namespace is this module's own name, as it is for every registering
// module: the list is collected over every registered module whether or not it
// runs, so two modules declaring one path is a startup failure even when only
// one of them ever runs.
//
// Every item takes its value from the primary config source alone. These are the
// two values that size the construction, and the source a host writes them into
// is the one the module reads them from; an environment variable or a
// command-line argument for one of them is not read, which the loader reports
// as a line nobody consumes. The tunables the loop reads while it runs are
// declared as settings instead.
func schema() config.Schema {
	item := func(description string) config.Item {
		return config.Item{Origins: config.OriginPrimary, Description: description}
	}
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			workersItem: item("how many jobs this process executes at once"),
			shutdownGraceItem: item("how long the stop stage waits for an execution under way before " +
				"cancelling it"),
		},
	}
}

// readSettings decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
func readSettings(reader config.Reader) (construction, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return construction{}, err
	}
	return cfg.resolve()
}
