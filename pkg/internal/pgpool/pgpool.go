// Package pgpool wraps the PostgreSQL connection pool that the key-value and
// event modules' postgres backends both need.
//
// It registers no module and delivers no capability. It exists because the two
// implementation subpackages would otherwise each carry their own copy of
// "parse a connection string, build a pool, refuse a size the pool cannot
// take": the two copies would differ only in where their configuration hangs,
// and neither could be covered by the other's tests. The cost of that sharing —
// one shared unit whose changes reach two modules — is recorded in
// docs/adr/adr-shared-backend-unit-2026-09-21.md.
//
// The pool type is exposed as it is rather than behind an abstraction of this
// package's own, so a caller that needs something this wrapper does not offer
// can reach it.
package pgpool

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vislake/speed/pkg/config"
)

// Config is the connection settings a mounting implementation declares under
// its own namespace.
//
// Both modules mount this same carrier, and each mounts it under its own path,
// so `kv.postgres.dsn` and `event.postgres.dsn` are separate input items that
// happen to accept the same shape.
type Config struct {
	// DSN is the connection string. Marked sensitive: it carries credentials,
	// and the logs and the help output would otherwise echo it. An absent one is
	// not how an assembly says it does not run this backend: a mounting
	// implementation is in the run because the command tree named it, so one
	// that has to reach PostgreSQL at a server nobody gave it stops the startup
	// and names this key.
	DSN string `config:"dsn"`
	// MaxConns is the largest number of connections the pool opens.
	MaxConns int `config:"max-conns"`
}

// defaultMaxConns is what the pool opens when the configuration names nothing.
const defaultMaxConns = 10

// Defaults returns the carrier a mounting implementation mounts as its
// prototype, filled with the values an absent key takes.
func Defaults() Config {
	return Config{MaxConns: defaultMaxConns}
}

// The input item paths, relative to a mounting implementation's namespace. They
// are held as constants because they appear twice over: once in the declaration
// and once in the text that names one to an operator.
const (
	DSNKey      = "dsn"
	MaxConnsKey = "max-conns"
)

// Items describes the connection settings, keyed by their path within the
// namespace a mounting implementation mounts them under.
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
//
// namespace is the mounting implementation's module name, and it is what the
// errors name so that an operator reads which backend refused a configuration.
// Building the pool does not confirm the server answers it — the pool connects
// lazily, and each backend does something over it right after this and fails
// there — so a pool that comes back has been assembled, not used.
func Connect(ctx context.Context, namespace string, cfg Config) (*pgxpool.Pool, error) {
	// The connection string is normalised once and everything below reads that
	// one value. A caller that judged a trimmed DSN usable — the modules'
	// stances do — would otherwise hand this function the untrimmed text, and
	// the parser would refuse a string this function had just accepted.
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		// pgx reads an empty connection string as the libpq defaults — PGHOST,
		// the default unix socket, the login name — and builds a pool from them
		// without reporting anything. A caller that reached this function with
		// nothing would be handed a live connection to whichever server answers
		// there, which is not the database the run was configured against.
		return nil, fmt.Errorf("%s: %s names no database, and this pool has to be pointed at one: "+
			"an empty connection string is not refused by the driver, it is read as the libpq "+
			"defaults", namespace, DSNKey)
	}
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: reading the connection string: %w", namespace, err)
	}
	if cfg.MaxConns > 0 {
		// A value past what the pool takes is refused rather than wrapped: a
		// negative one would be read as "no limit" or as an immediate refusal,
		// depending on the pool, and neither is what the host configured.
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
