// Package redis wraps the Redis client that the key-value and event modules
// both need.
//
// It registers no module and delivers no capability. It exists because two
// implementation subpackages would otherwise each carry their own copy of
// "read an address, build a client, close it": the two copies would differ only
// in where their configuration hangs, and neither could be covered by the
// other's tests. The cost of that sharing — one release unit whose changes
// reach two modules — is recorded in
// docs/adr/adr-shared-backend-unit-2026-09-21.md.
//
// The client type is exposed as it is rather than behind an abstraction of this
// package's own, so a caller that needs something this wrapper does not offer
// can reach it.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/vislake/speed/pkg/config"
)

// Config is the connection settings a mounting implementation declares under its
// own namespace.
//
// Both modules mount this same carrier, and each mounts it under its own path,
// so `kv.redis.addr` and `event.redis.addr` are separate input items that happen
// to accept the same shape.
type Config struct {
	// Addr is the host and port of the server. An absent one is how an assembly
	// says it does not run this backend, so the mounting implementation turns it
	// into a disabled module with a reason rather than a startup failure.
	Addr string `config:"addr"`
	// Password is the credential. Marked sensitive: it must not reach logs,
	// diagnostics or the help output.
	Password string `config:"password"`
	// DB is the logical database index. Redis separates keyspaces by number,
	// not by name.
	DB int `config:"db"`
	// PoolSize is the largest number of connections the client keeps. Zero
	// leaves the client's own default in place.
	PoolSize int `config:"pool-size"`
	// DialTimeout bounds establishing one connection, and ReadTimeout bounds a
	// single command's round trip. They are durations rather than counts of
	// seconds, so a bare number is refused rather than read as nanoseconds.
	DialTimeout time.Duration `config:"dial-timeout"`
	ReadTimeout time.Duration `config:"read-timeout"`
}

// The values an absent key takes. They are the client library's own defaults
// where it has one, written out so the mounted prototype carries them and the
// help output can show a caller what they are.
const (
	defaultDB          = 0
	defaultPoolSize    = 10
	defaultDialTimeout = 5 * time.Second
	defaultReadTimeout = 3 * time.Second
)

// Defaults returns the carrier a mounting implementation mounts as its
// prototype, filled with the values an absent key takes.
func Defaults() Config {
	return Config{
		DB:          defaultDB,
		PoolSize:    defaultPoolSize,
		DialTimeout: defaultDialTimeout,
		ReadTimeout: defaultReadTimeout,
	}
}

// The input item paths, relative to a mounting implementation's namespace. They
// are held as constants because they appear twice over: once in the declaration
// and once in the text that names one to an operator.
const (
	AddrKey     = "addr"
	PasswordKey = "password"
	DBKey       = "db"
	PoolSizeKey = "pool-size"
)

// Items describes the connection settings, keyed by their path within the
// namespace a mounting implementation mounts them under.
func Items() map[string]config.Item {
	return map[string]config.Item{
		AddrKey: {
			Description: "the host and port of the Redis server; leaving it out runs the assembly " +
				"without this backend",
		},
		PasswordKey: {
			Sensitive:   true,
			Description: "the password to authenticate with, when the server requires one",
		},
		DBKey: {
			Description: "the logical database index, which is how Redis separates keyspaces",
		},
		PoolSizeKey: {
			Description: "the largest number of connections the client keeps open",
		},
	}
}

// Connect builds a client and confirms the server answers it.
//
// The confirmation is part of the contract rather than a courtesy: an
// unreachable server has to fail the startup, and the alternative — returning a
// client that fails on its first command — turns a deployment mistake into a
// request-time error on whichever path happens to use the store first. A client
// built here and then found unusable is closed before the error is returned, so
// a failed startup leaves no pool behind.
func Connect(ctx context.Context, cfg Config) (*redis.Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis: the connection address is empty, and an assembly that runs " +
			"this backend has to name one")
	}
	client := redis.NewClient(&redis.Options{
		Addr:        cfg.Addr,
		Password:    cfg.Password,
		DB:          cfg.DB,
		PoolSize:    cfg.PoolSize,
		DialTimeout: cfg.DialTimeout,
		ReadTimeout: cfg.ReadTimeout,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: %s did not answer: %w", cfg.Addr, err)
	}
	return client, nil
}
