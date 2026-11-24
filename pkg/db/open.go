package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"
)

// openHandle opens one connection with an implementation's driver, sizes its pool from the configuration, and checks that the database answers, with the check bound to ctx; GORM's own contextless check on open is switched off. A failure at any step reports ErrConnectFailed, naming the dialect and the step and never the locator. A connection-scoped locator gets a pool of one connection that is never replaced; every handle classifies constraint refusals, carries the engine marker, logs through statementLogger, and openHandle panics on a nil logger.
func openHandle(ctx context.Context, spec Spec, cfg lockingConfig, logger *slog.Logger) (*gorm.DB, error) {
	// The stance judged the trimmed locator, so the driver gets the same text.
	locator := strings.TrimSpace(cfg.DSN)
	dialector := classifyingDialector{Dialector: spec.Dialector(locator), classify: spec.ConstraintViolation}
	session, err := gorm.Open(dialector, &gorm.Config{
		DisableAutomaticPing: true, TranslateError: true, Logger: newStatementLogger(logger),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: opening a %s connection failed: %w",
			ErrConnectFailed, spec.Dialect, err)
	}
	pool, err := session.DB()
	if err != nil {
		return nil, fmt.Errorf("%w: the %s session came back without a connection pool to size: %w",
			ErrConnectFailed, spec.Dialect, err)
	}
	// The three travel together: database/sql reads 0 as no limit and as a
	// connection that is never replaced.
	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if spec.ConnectionScoped != nil && spec.ConnectionScoped(locator) {
		// Retiring this connection would discard everything written through
		// the handle.
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pool.SetConnMaxLifetime(0)
		pool.SetConnMaxIdleTime(0)
	}
	if err := pool.PingContext(ctx); err != nil {
		return nil, errors.Join(
			fmt.Errorf("%w: the %s database did not answer the connectivity check: %w",
				ErrConnectFailed, spec.Dialect, err),
			closeUnusablePool(pool))
	}
	if err := installEncryption(session, spec.ConfigNamespace, cfg.Config); err != nil {
		return nil, errors.Join(err, Close(session))
	}
	if err := installRowClassification(session, spec.ConstraintViolation); err != nil {
		return nil, errors.Join(err, Close(session))
	}
	// Before the callers install the declared plugins, so a plugin's
	// Initialize already finds it.
	if err := markEngine(session, spec); err != nil {
		return nil, errors.Join(err, Close(session))
	}
	return session, nil
}

func closeUnusablePool(pool *sql.DB) error {
	if err := pool.Close(); err != nil {
		return fmt.Errorf("db: closing the pool of a connection that never answered failed: %w", err)
	}
	return nil
}
