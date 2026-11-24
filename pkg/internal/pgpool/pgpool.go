// Package pgpool wraps the PostgreSQL connection pool the key-value and event postgres backends share. It registers no module and delivers no capability.
package pgpool

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vislake/speed/pkg/config"
)

// Config is the connection settings a mounting implementation declares under its own namespace.
type Config struct {
	// DSN is the connection string.
	// Marked sensitive: it carries credentials, and the logs and the help output would otherwise echo
	// it.
	DSN string `config:"dsn"`
	// MaxConns is the largest number of connections the pool opens.
	MaxConns int `config:"max-conns"`
}

const defaultMaxConns = 10

// Defaults returns the carrier a mounting implementation mounts as its prototype, filled with the
// values an absent key takes.
func Defaults() Config {
	return Config{MaxConns: defaultMaxConns}
}

// The input item paths, relative to a mounting implementation's namespace.
// They are held as constants because they appear twice over: once in the declaration and once in
// the text that names one to an operator.
const (
	DSNKey      = "dsn"
	MaxConnsKey = "max-conns"
)

// Items describes the connection settings, keyed by their path within the namespace a mounting
// implementation mounts them under.
func Items() map[string]config.Item {
	return map[string]config.Item{
		DSNKey: {
			Sensitive: true,
			Description: "the PostgreSQL connection string; naming this implementation on the " +
				"command tree is what puts it in the run, and it has to be pointed at one, so " +
				"leaving this key out fails the startup",
		},
		MaxConnsKey: {
			Description: "the largest number of connections the pool opens",
		},
	}
}

// Connect builds the pool.
// namespace is the mounting implementation's module name, and it is what the errors name so that
// an operator reads which backend refused a configuration.
func Connect(ctx context.Context, namespace string, cfg Config) (*pgxpool.Pool, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("%s: %s names no database, and this pool has to be pointed at one: "+
			"an empty connection string is not refused by the driver, it is read as the libpq "+
			"defaults", namespace, DSNKey)
	}
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: reading the connection string: %w", namespace, err)
	}
	if cfg.MaxConns > 0 {
		if cfg.MaxConns > math.MaxInt32 {
			return nil, fmt.Errorf("%s: %s is %d, past what a connection pool accepts",
				namespace, MaxConnsKey, cfg.MaxConns)
		}
		poolCfg.MaxConns = int32(cfg.MaxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("%s: building the pool: %w", namespace, err)
	}
	return pool, nil
}
