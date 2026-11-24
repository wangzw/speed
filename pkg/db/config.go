package db

import (
	"time"

	"github.com/vislake/speed/pkg/config"
)

// Config is the set of input items every implementation of the database
// capability accepts, mounted under the implementation's own namespace.
type Config struct {
	// DSN is the connection locator, in the form the implementation's own driver reads. It is marked sensitive and an assembly that names this implementation has to set it: Prepare fails the startup naming this key when it does not.
	DSN string `config:"dsn"`
	// MaxOpenConns is the largest number of connections the pool opens. Its
	// default is 25.
	MaxOpenConns int `config:"max-open-conns"`
	// MaxIdleConns is how many idle connections the pool keeps. Its default is 5.
	MaxIdleConns int `config:"max-idle-conns"`
	// ConnMaxLifetime is how long one connection is reused before it is
	// replaced. Its default is 30 minutes.
	ConnMaxLifetime time.Duration `config:"conn-max-lifetime"`
	// EncryptionKey is the root key the field encryption and blind index subkeys derive from; marked sensitive. An absent key is not a startup failure: encryption and decryption fail on the statement that reaches an encrypted field. A present key shorter than 14 bytes, or whitespace only, fails the assembly.
	EncryptionKey string `config:"encryption-key"`
	// EncryptionRetiredKeys are root keys that are no longer written with but
	// still decrypt what they wrote. Marked sensitive.
	EncryptionRetiredKeys []string `config:"encryption-retired-keys"`
}

type lockingConfig struct {
	Config
	// MigrationLockTimeout bounds the wait for the migration mutex; past it the
	// startup fails with ErrMigrationLockTimeout. It has to be positive.
	MigrationLockTimeout time.Duration `config:"migration-lock-timeout"`
}

const (
	dsnKey                   = "dsn"
	maxOpenConnsKey          = "max-open-conns"
	maxIdleConnsKey          = "max-idle-conns"
	connMaxLifetimeKey       = "conn-max-lifetime"
	encryptionKeyKey         = "encryption-key"
	encryptionRetiredKeysKey = "encryption-retired-keys"
)

// MigrationLockTimeoutKey names the input item bounding the wait for the migration mutex, relative to an implementation's configuration namespace.
const MigrationLockTimeoutKey = "migration-lock-timeout"

const (
	defaultMaxOpenConns    = 25
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = 30 * time.Minute
)

// defaults returns the carrier this implementation's items are declared with, filled with the values an absent key takes. The pool parameters must not land on 0: database/sql reads that as no limit.
func (s Spec) defaults() lockingConfig {
	return lockingConfig{
		Config: Config{
			MaxOpenConns:    defaultMaxOpenConns,
			MaxIdleConns:    defaultMaxIdleConns,
			ConnMaxLifetime: defaultConnMaxLifetime,
		},
		MigrationLockTimeout: s.DefaultMigrationLockTimeout,
	}
}

// schema is this implementation's input item declaration, mounted under its own
// namespace. No item takes a command-line argument: a locator and a root key
// must not appear in a process listing.
func (s Spec) schema() config.Schema {
	proto := s.defaults()
	mount := config.Mount{Value: &proto.Config}
	items := map[string]config.Item{
		dsnKey: {
			Sensitive: true,
			Description: "the locator of the database to connect to, in the form this engine's " +
				"own driver reads; an assembly that names this engine on its command tree has to " +
				"set it",
		},
		maxOpenConnsKey: {
			Description: "the largest number of connections the pool opens",
		},
		maxIdleConnsKey: {
			Description: "how many idle connections the pool keeps open",
		},
		connMaxLifetimeKey: {
			Description: "how long one connection is reused before it is replaced, written with " +
				"a unit, such as 30m",
		},
		encryptionKeyKey: {
			Sensitive: true,
			Description: "the root key the field encryption and blind index subkeys are derived " +
				"from, at least 14 bytes long; without it every encrypted field fails on use rather " +
				"than at startup",
		},
		encryptionRetiredKeysKey: {
			Sensitive: true,
			Description: "root keys that are no longer written with and still decrypt what they " +
				"wrote, given as a list",
		},
	}
	if s.NewMigrationLock != nil {
		mount = config.Mount{Value: &proto}
		items[MigrationLockTimeoutKey] = config.Item{
			Description: "how long to wait for another replica to finish applying migrations " +
				"before giving up, a positive duration written with a unit, such as 5m",
		}
	}
	return config.Schema{
		Namespace: s.ConfigNamespace,
		Mounts:    []config.Mount{mount},
		Items:     items,
	}
}

func (s Spec) read(reader config.Reader) (lockingConfig, error) {
	cfg := s.defaults()
	if err := reader.Decode(s.ConfigNamespace, &cfg); err != nil {
		return lockingConfig{}, err
	}
	return cfg, nil
}
