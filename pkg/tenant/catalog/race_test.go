package catalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/tenant"
)

// rosterOverPostgres is a roster over the PostgreSQL fixture, its table built
// from the migration the module ships for that dialect.
//
// Its pool is held to a few connections. The cases over it race many writes at
// once, and a handful of connections is enough for their transactions to
// overlap; a connection per goroutine would crowd the fixture's connection
// limit, which every package's cases share, and fail with the server's
// refusal to connect rather than with anything the case is about.
func rosterOverPostgres(t *testing.T) *roster {
	t.Helper()
	handle, err := gorm.Open(pgdriver.Open(pgtest.Acquire(t)),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	pool.SetMaxOpenConns(racingConnections)
	ddl, err := fs.ReadFile(migrationsFS, "postgres/0001_tenants.sql")
	if err != nil {
		t.Fatalf("reading the postgres migration: %v", err)
	}
	if err := handle.Exec(string(ddl)).Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	return newRoster(settings{}, databaseOver{handle: handle})
}

// racingConnections bounds the pool of a roster over the PostgreSQL fixture.
const racingConnections = 8

// Two tenants put under one alias at once both find it free, and the second
// insert meets the column's unique index. That is the same conflict the check
// inside the write reports, and the caller is owed the same answer for it:
// ErrAliasConflict naming the holder, not the driver's constraint violation.
func TestAConcurrentAliasConflictIsReportedAsOne(t *testing.T) {
	r := rosterOverPostgres(t)
	ctx := context.Background()

	const pairs = 50
	var wg sync.WaitGroup
	errs := make([][2]error, pairs)
	for i := 0; i < pairs; i++ {
		for side, prefix := range []string{"a", "b"} {
			wg.Add(1)
			go func(i, side int, prefix string) {
				defer wg.Done()
				errs[i][side] = r.Put(ctx, tenant.Tenant{
					ID:     tenant.ID(fmt.Sprintf("%s_%d", prefix, i)),
					Alias:  fmt.Sprintf("alias_%d", i),
					Status: tenant.StatusActive,
				})
			}(i, side, prefix)
		}
	}
	wg.Wait()

	var other []error
	for i, pair := range errs {
		failed := 0
		for _, err := range pair {
			switch {
			case err == nil:
			case errors.Is(err, tenant.ErrAliasConflict):
				failed++
			default:
				failed++
				other = append(other, err)
			}
		}
		if failed != 1 {
			t.Fatalf("pair %d under one alias ended %v, want exactly one refused", i, pair)
		}
	}
	if len(other) > 0 {
		t.Fatalf("%d of %d concurrent conflicts were not reported as ErrAliasConflict; the first: %v",
			len(other), pairs, other[0])
	}
}
