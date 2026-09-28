package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	if err != nil || !ok || got != want {
		t.Fatalf("the writer reads (%+v, %v, %v) back, want %+v", got, ok, err, want)
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

// arriveTogether holds the first n inserts into the roster's table until all n
// have arrived, so that n writes have each looked the table up before any of
// them inserts — the interleaving a race produces only some of the time.
func arriveTogether(t *testing.T, r *roster, n int) {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	err := r.handle.Callback().Create().Before("gorm:create").Register("test:arrive-together",
		func(tx *gorm.DB) {
			if tx.Statement.Table != tableName {
				return
			}
			mu.Lock()
			arrived++
			if arrived == n {
				close(all)
			}
			late := arrived > n
			mu.Unlock()
			if late {
				return
			}
			select {
			case <-all:
			case <-time.After(10 * time.Second):
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
}

// Creations of one identifier on two replicas all find it free before any has
// committed. The table decides: one tenant, and every other creation told
// ErrTenantExists rather than the driver's constraint violation — or, worse, a
// success that rewrote the winner's row.
func TestConcurrentCreationsOfOneIdentifierAreOneTenant(t *testing.T) {
	first := rosterOverPostgres(t)
	if err := first.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	replicas := []*roster{first, secondReplica(t, first)}

	const writers = 4
	arriveTogether(t, first, writers)
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = replicas[i%2].Create(context.Background(), tenant.Tenant{
				ID: "t_acme", Alias: fmt.Sprintf("acme_%d", i), Status: tenant.StatusActive,
			})
		}(i)
	}
	wg.Wait()

	won := -1
	for i, err := range errs {
		switch {
		case err == nil && won < 0:
			won = i
		case err == nil:
			t.Fatalf("creations %d and %d of one identifier both succeeded", won, i)
		case !errors.Is(err, tenant.ErrTenantExists):
			t.Fatalf("creation %d gave %v, want ErrTenantExists", i, err)
		}
	}
	if won < 0 {
		t.Fatal("no creation of the identifier succeeded")
	}
	held, ok := rowOf(t, first, "t_acme")
	if !ok || held.Alias != fmt.Sprintf("acme_%d", won) {
		t.Fatalf("the table holds %+v, want the row creation %d wrote", held, won)
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
