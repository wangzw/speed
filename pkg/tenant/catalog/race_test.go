//go:build integration

package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/internal/dbtest"
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
	handle := quiet(dbtest.Postgres(t, pgtest.Acquire(t), dbtest.Items{
		"max-open-conns": racingConnections, "max-idle-conns": racingConnections,
	}).DB())
	applyMigrations(t, handle, "postgres")
	return newRoster(settings{}, databaseOver{handle: handle})
}

// racingConnections bounds the pool of a roster over the PostgreSQL fixture.
const racingConnections = 8

// A table dropped since the load is told apart from any other failure on
// PostgreSQL too, where a statement that fails inside a transaction leaves the
// transaction taking nothing further: the question that tells them apart is
// asked outside it, so a write over the dropped table reports ErrRosterMissing
// as an entry and a reread of the whole roster do.
func TestADroppedTableIsReportedMissingOnPostgres(t *testing.T) {
	r := rosterOverPostgres(t)
	if err := r.handle.Exec(insert("t_acme", "acme")).Error; err != nil {
		t.Fatalf("writing a row: %v", err)
	}
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}
	if err := r.handle.Exec("DROP TABLE " + tableName).Error; err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"Put": func() error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"Create": func() error {
			return r.Create(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func() error { return r.SetStatus(ctx, "t_acme", tenant.StatusDisabled) },
		"Enter": func() error {
			_, err := r.Enter(ctx, "t_ghost")
			return err
		},
		"a reread of the whole roster": func() error { return r.read(ctx) },
	} {
		if err := call(); !errors.Is(err, tenant.ErrRosterMissing) {
			t.Errorf("%s over a dropped table gave %v, want ErrRosterMissing", name, err)
		}
	}
}

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
					Alias:  fmt.Sprintf("alias-%d", i),
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
