// Package redis wraps the Redis client the key-value and event modules share. It registers no module and delivers no capability.
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

// Config is the connection settings a mounting implementation declares under its own namespace.
// Both modules mount this same carrier, and each mounts it under its own path, so `kv.redis.addr`
// and `event.redis.addr` are separate input items that happen to accept the same shape.
type Config struct {
	// Addr is the host and port of the server.
	Addr string `config:"addr"`
	// Password is the credential.
	// Marked sensitive: it must not reach logs, diagnostics or the help output.
	Password string `config:"password"`
	// DB is the logical database index.
	// Redis separates keyspaces by number, not by name.
	DB int `config:"db"`
	// PoolSize is the largest number of connections the client keeps.
	// Zero leaves the client's own default in place.
	PoolSize int `config:"pool-size"`
	// DialTimeout bounds establishing one connection, and ReadTimeout bounds a single command's round
	// trip.
	DialTimeout time.Duration `config:"dial-timeout"`
	// ReadTimeout is the read timeout for the client.
	ReadTimeout time.Duration `config:"read-timeout"`
}

// The values an absent key takes. DB, DialTimeout and ReadTimeout are the client library's own
// defaults; PoolSize is not, because the library defaults it to 10 per GOMAXPROCS while this
// module pins 10 whatever the core count.
const (
	defaultDB          = 0
	defaultPoolSize    = 10
	defaultDialTimeout = 5 * time.Second
	defaultReadTimeout = 3 * time.Second
)

// Defaults returns the carrier a mounting implementation mounts as its prototype, filled with the
// values an absent key takes.
func Defaults() Config {
	return Config{
		DB:          defaultDB,
		PoolSize:    defaultPoolSize,
		DialTimeout: defaultDialTimeout,
		ReadTimeout: defaultReadTimeout,
	}
}

// The input item paths, relative to a mounting implementation's namespace.
// They are held as constants because they appear twice over: once in the declaration and once in
// the text that names one to an operator.
const (
	AddrKey        = "addr"
	PasswordKey    = "password"
	DBKey          = "db"
	PoolSizeKey    = "pool-size"
	DialTimeoutKey = "dial-timeout"
	ReadTimeoutKey = "read-timeout"
)

// Items describes the connection settings, keyed by their path within the namespace a mounting
// implementation mounts them under.
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
func Connect(ctx context.Context, cfg Config) (*redis.Client, error) {
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
		MaxRetries:  -1,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: %s did not answer: %w", addr, err)
	}
	return client, nil
}
