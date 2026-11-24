package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
)

type database struct {
	spec    Spec
	cfg     lockingConfig
	handle  *gorm.DB
	logger  *slog.Logger
	plugins []pluginInstall
	digest  func(plaintext string) string
}

var _ Database = (*database)(nil)

func (s Spec) newDatabase(ctx context.Context, reader config.Reader, plugins []pluginInstall,
	logger *slog.Logger,
) (*database, error) {
	cfg, err := s.read(reader)
	if err != nil {
		return nil, err
	}
	handle, err := openHandle(ctx, s, cfg, logger)
	if err != nil {
		return nil, err
	}
	digest, err := digestFor(s.ConfigNamespace, cfg.Config)
	if err != nil {
		return nil, errors.Join(err, Close(handle))
	}
	// Before the product exists, so no caller can issue a statement the
	// declared plugins were meant to reach without them.
	if err := installPlugins(handle, plugins); err != nil {
		return nil, errors.Join(err, Close(handle))
	}
	return &database{
		spec:    s,
		cfg:     cfg,
		handle:  handle,
		logger:  logger,
		plugins: plugins,
		digest:  digest,
	}, nil
}

func (d *database) DB() *gorm.DB { return d.handle }

func (d *database) Dialect() Dialect { return d.spec.Dialect }

func (d *database) BlindIndex(plaintext string) string {
	if d.digest == nil {
		panic("db: BlindIndex was called on a database assembled without the key " +
			"derivation it works under")
	}
	return d.digest(plaintext)
}

func (d *database) Open(ctx context.Context, dsn string) (*gorm.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: Open was handed a blank %s locator, and a connection this module "+
			"builds has to name the database it reaches", ErrConnectFailed, d.spec.Dialect)
	}
	cfg := d.cfg
	cfg.DSN = dsn
	handle, err := openHandle(ctx, d.spec, cfg, d.logger)
	if err != nil {
		return nil, err
	}
	if err := markOpened(handle, d.handle); err != nil {
		return nil, errors.Join(err, Close(handle))
	}
	if err := installPlugins(handle, d.plugins); err != nil {
		return nil, errors.Join(err, Close(handle))
	}
	return handle, nil
}
