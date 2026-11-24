package sms

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/sms/budget"
)

// TestStartRefusesWithoutARootKey checks that Start refuses without a root key.
func TestStartRefusesWithoutARootKey(t *testing.T) {
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), nil)
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, sqliteDialect)

	p := build(database, newRegistry(), newSender(), budget.New(database.DB()), resolved{Workers: 1, QueueSize: 4}, testDefaultLocale,
		templateLocales(newRegistry().Declared()))
	err := p.start(context.Background())
	if !errors.Is(err, authn.ErrNoEncryptionKey) {
		t.Fatalf("start without a root key returned %v, want ErrNoEncryptionKey", err)
	}
}

// TestStartRefusesWhenTheTableIsMissing checks that Start refuses when the table is missing.
func TestStartRefusesWhenTheTableIsMissing(t *testing.T) {
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), nil)
	p := build(database, newRegistry(), newSender(), budget.New(database.DB()), resolved{Workers: 1, QueueSize: 4}, testDefaultLocale,
		templateLocales(newRegistry().Declared()))
	err := p.start(context.Background())
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Fatalf("start without the table returned %v, want ErrNotMigrated", err)
	}
}
