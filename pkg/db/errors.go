package db

import "errors"

var (
	// ErrConnectFailed reports that the driver would not open, or that the
	// connectivity check did not pass. Its text names the dialect and the step
	// and never echoes the locator, which carries credentials.
	ErrConnectFailed = errors.New("db: connection could not be established")
	// ErrPluginFailed reports that a declared plugin did not install.
	ErrPluginFailed = errors.New("db: declared plugin failed to install")
	// ErrMigrationFailed reports that a migration did not apply, that the
	// migration record table could not be read or written, or that the
	// migration mutex could not be given up.
	ErrMigrationFailed = errors.New("db: migration could not be applied")
	// ErrMigrationLockTimeout reports that the migration mutex was not
	// acquired within migration-lock-timeout.
	ErrMigrationLockTimeout = errors.New("db: migration mutex was not acquired before the timeout")
	// ErrForeignInstance reports that Migrate was handed an instance this
	// module's own New did not build. It is refused before the migration mutex
	// is taken, so no migration was applied.
	ErrForeignInstance = errors.New("db: supplied instance was not built by this module's New")
)
