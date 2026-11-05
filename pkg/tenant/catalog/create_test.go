package catalog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about registering a tenant that must not
// overwrite one, and about what a write owes its caller once it has committed.

// secondReplica is another process's roster over the same table: it read the
// table when it started and has not read it since.
func secondReplica(t *testing.T, over *roster) *roster {
	t.Helper()
	replica := newRoster(settings{}, databaseOver{handle: over.handle})
	if err := replica.read(context.Background()); err != nil {
		t.Fatalf("reading the roster on the second replica: %v", err)
	}
	return replica
}

func TestCreateRegistersATenantTheWriterSeesAtOnce(t *testing.T) {
	r := rosterOver(t)
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	want := tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
	if err := r.Create(context.Background(), want); err != nil {
		t.Fatalf("creating: %v", err)
	}
	got, ok, err := r.Get(context.Background(), "t_acme")
	if err != nil || !ok {
		t.Fatalf("the writer reads (%+v, %v, %v) back, want the tenant", got, ok, err)
	}
	if got.ID != want.ID || got.Alias != want.Alias || got.Status != want.Status ||
		got.DisabledBy != want.DisabledBy || got.Profile != want.Profile {
		t.Fatalf("the writer reads (%+v, %v, %v) back, want %+v", got, ok, err, want)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("the writer reads no creation time back, want the moment of creation")
	}
}

// A replica whose snapshot predates another replica's registration is asked
// to register the same identifier. The table holds it, so the creation is
// refused and the tenant keeps the alias and the status it has — a disabled
// tenant stays disabled, which is what a registration by Put would undo.
func TestCreateRefusesAnIdentifierTheTableHoldsWhateverTheSnapshotSays(t *testing.T) {
	first := rosterOver(t)
	if err := first.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	stale := secondReplica(t, first)

	ctx := context.Background()
	if err := first.Create(ctx, tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("creating on the first replica: %v", err)
	}
	if err := first.SetStatus(ctx, "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling on the first replica: %v", err)
	}
	if _, ok, _ := stale.Get(ctx, "t_acme"); ok {
		t.Fatal("the second replica's snapshot already holds the tenant; the case needs one that does not")
	}

	err := stale.Create(ctx, tenant.Tenant{ID: "t_acme", Alias: "acme-again", Status: tenant.StatusActive})
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("creating an identifier the table holds gave %v, want ErrTenantExists", err)
	}
	held, ok := rowOf(t, first, "t_acme")
	if !ok || held.Alias != "acme" || held.Status != int(tenant.StatusDisabled) {
		t.Fatalf("the refused creation left the row as %+v, want alias acme and disabled", held)
	}
}

// The alias is refused as Put refuses it, and nothing is written.
func TestCreateRefusesAnAliasAnotherTenantHolds(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	err := r.Create(context.Background(), tenant.Tenant{ID: "t_other", Alias: "acme", Status: tenant.StatusActive})
	if !errors.Is(err, tenant.ErrAliasConflict) {
		t.Fatalf("creating under an alias another tenant holds gave %v, want ErrAliasConflict", err)
	}
	if _, ok := rowOf(t, r, "t_other"); ok {
		t.Fatal("the refused creation wrote a row")
	}
}

// Registering an identifier twice is refused even when the second creation
// repeats the first word for word: a creation that must not overwrite cannot
// tell a retry from a second registrant, and says which it met.
func TestCreateRefusesTheSameTenantTwice(t *testing.T) {
	r := rosterOver(t)
	same := tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
	if err := r.Create(context.Background(), same); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := r.Create(context.Background(), same); !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("creating the same tenant again gave %v, want ErrTenantExists", err)
	}
}

func TestCreateRefusesATenantOutsideTheIdentifierDomain(t *testing.T) {
	r := rosterOver(t)
	for _, bad := range []tenant.Tenant{
		{ID: "", Alias: "acme", Status: tenant.StatusActive},
		{ID: "t acme", Alias: "acme", Status: tenant.StatusActive},
		{ID: "t_acme", Alias: "", Status: tenant.StatusActive},
	} {
		if err := r.Create(context.Background(), bad); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("creating %+v gave %v, want ErrInvalidTenant", bad, err)
		}
	}
}

// cancelOnReread cancels the caller's context the moment a write, having
// committed, starts rereading the roster — the one read of the table that
// names no row.
func cancelOnReread(t *testing.T, r *roster, cancel context.CancelFunc) {
	t.Helper()
	err := r.handle.Callback().Query().Before("gorm:query").Register("test:cancel-on-reread",
		func(tx *gorm.DB) {
			if _, conditioned := tx.Statement.Clauses["WHERE"]; !conditioned && tx.Statement.Table == tableName {
				cancel()
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
}

// A caller that goes away once its write has committed has nothing left to
// stop. The reread that serves the writer its own write carries on, so the
// write is reported as the success it was and this replica serves it at once.
func TestAWriteIsServedWhenItsCallerGoesAwayAfterTheCommit(t *testing.T) {
	for name, write := range map[string]func(ctx context.Context, r *roster) error{
		"Put": func(ctx context.Context, r *roster) error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"Create": func(ctx context.Context, r *roster) error {
			return r.Create(ctx, tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func(ctx context.Context, r *roster) error {
			return r.SetStatus(ctx, "t_new", tenant.StatusActive)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, "INSERT INTO tenants (id, alias, status) VALUES ('t_old', 'old', 1)")
			if name == "SetStatus" {
				if err := r.handle.Exec("INSERT INTO tenants (id, alias, status) VALUES ('t_new', 'new', 0)").
					Error; err != nil {
					t.Fatalf("seeding: %v", err)
				}
			}
			if err := r.read(context.Background()); err != nil {
				t.Fatalf("reading: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelOnReread(t, r, cancel)

			if err := write(ctx, r); err != nil {
				t.Fatalf("a write whose caller went away after the commit gave %v, want success", err)
			}
			got, ok, err := r.Get(context.Background(), "t_new")
			if err != nil || !ok || got.Status != tenant.StatusActive {
				t.Fatalf("the writer's replica serves (%+v, %v, %v), want the write", got, ok, err)
			}
		})
	}
}

// holdFirstFullRead holds the first read of the whole roster table from here
// on, once it has the table's rows and before it publishes them, until release
// is closed. The channel it returns is closed once that read is being held.
func holdFirstFullRead(t *testing.T, r *roster, release <-chan struct{}) <-chan struct{} {
	t.Helper()
	held := make(chan struct{})
	var taken atomic.Bool
	err := r.handle.Callback().Query().After("gorm:query").Register("test:hold-first-full-read",
		func(tx *gorm.DB) {
			if _, conditioned := tx.Statement.Clauses["WHERE"]; conditioned || tx.Statement.Table != tableName {
				return
			}
			if taken.CompareAndSwap(false, true) {
				close(held)
				<-release
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
	return held
}

// A refresh that read the table before a write committed, and publishes only
// after the write's own reread has, would put the roster without the write back
// in force on the writer's replica — a tenant just disabled served again until
// the next refresh. The reads of the roster publish in the order they read.
func TestARefreshThatReadBeforeAWriteDoesNotUndoIt(t *testing.T) {
	r := rosterOver(t, insert("t_old", "old"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	release := make(chan struct{})
	held := holdFirstFullRead(t, r, release)
	refreshed := make(chan struct{})
	go func() {
		defer close(refreshed)
		r.refreshOnce(context.Background())
	}()
	<-held

	written := make(chan error, 1)
	go func() {
		written <- r.Create(context.Background(), tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusActive})
	}()
	var err error
	done := false
	select {
	case err = <-written:
		done = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-refreshed
	if !done {
		err = <-written
	}
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	if got, ok, err := r.Get(context.Background(), "t_new"); err != nil || !ok {
		t.Fatalf("after the refresh the writer's replica serves (%+v, %v, %v), want its own write", got, ok, err)
	}
}
