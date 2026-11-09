//go:build integration

package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases here hold the terminal state against a write that read the row
// before the deletion committed and writes after it. Each write path carries the
// tombstone condition in its own statement, so the statement updates no row and
// the transaction reads the row again to answer ErrTenantDeleted. The
// interleaving is arranged rather than raced: a connection of this test's own
// deletes the row without committing, the writer reads the row as it was, and
// the deletion commits while the writer's statement waits for the row lock.

// awaitBlockedWriter waits until a backend of this database is waiting for a
// row lock. That is the moment the writer has read the row and is updating it:
// the read does not take a lock, so the wait cannot begin before it.
func awaitBlockedWriter(t *testing.T, r *roster, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("the write answered before it could be held: %v", err)
		default:
		}
		var waiting int64
		if err := r.handle.Raw(
			"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'",
		).Scan(&waiting).Error; err != nil {
			t.Fatalf("looking for the blocked write: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the write never waited on the row the deletion held, so the interleaving was not reproduced")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAConcurrentDeletionIsNotOverwrittenOnPostgres holds the terminal state
// against every write path that reads the row and then updates it by identifier.
// A deletion commits between the two, and the write's own statement — which
// carries the tombstone condition — must leave the row as the deletion wrote it:
// the status stays deleted, the name stays the deleted row's, and the caller is
// answered ErrTenantDeleted.
func TestAConcurrentDeletionIsNotOverwrittenOnPostgres(t *testing.T) {
	for name, write := range map[string]func(context.Context, *roster) error{
		"SetStatus disabled": func(ctx context.Context, r *roster) error {
			return r.SetStatus(ctx, "t_acme", tenant.StatusDisabled)
		},
		"SetStatus active": func(ctx context.Context, r *roster) error {
			return r.SetStatus(ctx, "t_acme", tenant.StatusActive)
		},
		"SetStatusByAccount disabled": func(ctx context.Context, r *roster) error {
			return r.SetStatusByAccount(ctx, "t_acme", tenant.StatusDisabled)
		},
		"Put": func(ctx context.Context, r *roster) error {
			return r.Put(ctx, tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive})
		},
		"SetOwner": func(ctx context.Context, r *roster) error {
			return r.SetOwner(ctx, "t_acme", "acct-2")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOverPostgres(t)
			ctx := context.Background()
			if err := r.handle.Exec(insert("t_acme", "acme")).Error; err != nil {
				t.Fatalf("writing the row: %v", err)
			}
			if err := r.read(ctx); err != nil {
				t.Fatalf("reading the roster: %v", err)
			}

			holder := r.handle.Begin()
			if holder.Error != nil {
				t.Fatalf("opening the deleting transaction: %v", holder.Error)
			}
			defer func() { _ = holder.Rollback().Error }()
			if err := holder.Exec("UPDATE "+tableName+" SET status = ? WHERE id = ?",
				int(tenant.StatusDeleted), "t_acme").Error; err != nil {
				t.Fatalf("holding the deletion open: %v", err)
			}

			errs := make(chan error, 1)
			go func() { errs <- write(ctx, r) }()
			awaitBlockedWriter(t, r, errs)
			if err := holder.Commit().Error; err != nil {
				t.Fatalf("committing the deletion: %v", err)
			}

			if err := <-errs; !errors.Is(err, tenant.ErrTenantDeleted) {
				t.Fatalf("%s over a deletion committed mid-write gave %v, want ErrTenantDeleted", name, err)
			}
			requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusDeleted)})
		})
	}
}
