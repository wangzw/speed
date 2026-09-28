// Package redis wraps the Redis client that the key-value and event modules
// both need.
//
// It registers no module and delivers no capability. It exists because two
// implementation subpackages would otherwise each carry their own copy of
// "read an address, build a client, close it": the two copies would differ only
// in where their configuration hangs, and neither could be covered by the
// other's tests. The cost of that sharing — one shared unit whose changes
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
	"strings"
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
	// Addr is the host and port of the server. An absent one is not how an
	// assembly says it does not run this backend: a mounting implementation is
	// in the run because the command tree named it, so one that has to reach
	// Redis at a server nobody gave it stops the startup and names this key.
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
	AddrKey        = "addr"
	PasswordKey    = "password"
	DBKey          = "db"
	PoolSizeKey    = "pool-size"
	DialTimeoutKey = "dial-timeout"
	ReadTimeoutKey = "read-timeout"
)

// Items describes the connection settings, keyed by their path within the
// namespace a mounting implementation mounts them under.
func Items() map[string]config.Item {
	return map[string]config.Item{
		AddrKey: {
			Description: "the host and port of the Redis server; naming this implementation on the " +
				"command tree is what puts it in the run, and it has to be pointed at one, so " +
				"leaving this key out fails the startup",
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
		DialTimeoutKey: {
			Description: "how long establishing one connection may take",
		},
		ReadTimeoutKey: {
			Description: "how long a single command's round trip may take",
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
	// The address is normalised once and everything below reads that one
	// value. A caller that judged a trimmed address usable — the modules'
	// stances do — would otherwise hand this function the untrimmed text, and
	// the client would refuse an address this function had just accepted.
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, errors.New("redis: the connection address is empty, and an assembly that runs " +
			"this backend has to name one")
	}
	client := redis.NewClient(&redis.Options{
		Addr:        addr,
		Password:    cfg.Password,
		DB:          cfg.DB,
		PoolSize:    cfg.PoolSize,
		DialTimeout: cfg.DialTimeout,
		ReadTimeout: cfg.ReadTimeout,
		// The client retries nothing. Left at its zero value, go-redis
		// retries a command up to three times when its reply is lost — the
		// connection dropping after the write, or the read timing out — and a
		// command whose reply was lost may well have run: an increment would
		// count twice and report the doubled value, a compare-and-swap that
		// won would re-run and report that it lost, and a stream entry would
		// be appended twice. Whether an operation is safe to repeat is the
		// caller's to know, so the failure goes back to the caller.
		MaxRetries: -1,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: %s did not answer: %w", addr, err)
	}
	return client, nil
}
