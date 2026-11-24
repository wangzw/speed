package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
)

// ErrInvalidConfig reports a configuration this module cannot run on: a worker
// count or a shutdown grace outside the range the queue can work with.
var ErrInvalidConfig = errors.New("jobs.db: the configuration holds a value the queue cannot run on")

const (
	workersItem       = "workers"
	shutdownGraceItem = "shutdown-grace"
)

var configDefaults = moduleConfig{
	Workers:       4,
	ShutdownGrace: 10 * time.Second,
}

type moduleConfig struct {
	Workers       int           `config:"workers"`
	ShutdownGrace time.Duration `config:"shutdown-grace"`
}

type construction struct {
	workers int
	grace   time.Duration
}

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

func readSettings(reader config.Reader) (construction, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return construction{}, err
	}
	return cfg.resolve()
}
