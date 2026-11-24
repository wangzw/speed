package catalog

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

func enteringRoster(t *testing.T) *roster {
	t.Helper()
	r := rosterOver(t,
		insert("t_acme", "acme"),
		insert("t_globex", "globex"),
		"INSERT INTO tenants (id, name, status) VALUES ('t_paused', 'paused', 0)",
		insertRow("t_deleted", "deleted", tenant.StatusDeleted, tenant.DisabledByOperator))
	if err := r.handle.Use(newFilter()); err != nil {
		t.Fatalf("installing the filter: %v", err)
	}
	if err := r.handle.Exec(ordersDDL).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	if err := r.handle.WithContext(crossContext()).Exec(
		"INSERT INTO orders (tenant_id, id, note) VALUES ('t_acme', 'a', 'x'), ('t_globex', 'g', 'y')").
		Error; err != nil {
		t.Fatalf("seeding orders: %v", err)
	}
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}
	return r
}

func requireScopedTo(t *testing.T, r *roster, ctx context.Context, want tenant.ID) {
	t.Helper()
	if id, ok := tenant.FromContext(ctx); !ok || id != want {
		t.Fatalf("the entered context reports tenant (%q, %v), want %q", id, ok, want)
	}
	if tenantctx.CrossTenant(ctx) {
		t.Fatal("the entered context still crosses tenants")
	}
	var got []order
	if err := r.handle.WithContext(ctx).Find(&got).Error; err != nil {
		t.Fatalf("reading under the entered context: %v", err)
	}
	for _, o := range got {
		if o.TenantID != want {
			t.Fatalf("a read under the entered context returned %+v, want only %s's rows", got, want)
		}
	}
	if len(got) != 1 {
		t.Fatalf("a read under the entered context returned %d rows, want %s's one", len(got), want)
	}
	if err := r.handle.WithContext(ctx).Create(&order{ID: "new"}).Error; err != nil {
		t.Fatalf("inserting under the entered context: %v", err)
	}
	requireRowTenant(t, r.handle, "new", string(want))
}

func TestEnterScopesAPathToTheTenant(t *testing.T) {
	r := enteringRoster(t)
	ctx, err := r.Enter(context.Background(), "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	requireScopedTo(t, r, ctx, "t_acme")
}

func TestEnterFromACrossingContextLiftsTheMark(t *testing.T) {
	r := enteringRoster(t)
	ctx, err := r.Enter(crossContext(), "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	requireScopedTo(t, r, ctx, "t_acme")
}

func TestEnterReplacesTheTenantAContextCarried(t *testing.T) {
	r := enteringRoster(t)
	ctx, err := r.Enter(scopedContext("t_globex"), "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	requireScopedTo(t, r, ctx, "t_acme")
}

func TestEnterRefusesATenantTheRosterDoesNotServe(t *testing.T) {
	for _, c := range []struct {
		name string
		id   tenant.ID
		want error
	}{
		{"no row carries it", "t_ghost", tenant.ErrInvalidTenant},
		{"disabled", "t_paused", tenant.ErrTenantDisabled},
		{"empty", "", tenant.ErrInvalidTenant},
		{"outside the domain", "t acme", tenant.ErrInvalidTenant},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := enteringRoster(t)
			ctx, err := r.Enter(crossContext(), c.id)
			if !errors.Is(err, c.want) {
				t.Fatalf("entering %q gave %v, want %v", c.id, err, c.want)
			}
			if id, ok := tenant.FromContext(ctx); ok || tenantctx.CrossTenant(ctx) {
				t.Fatalf("a refused entry handed back tenant (%q, %v), crossing %v; want neither",
					id, ok, tenantctx.CrossTenant(ctx))
			}
			var got []order
			if err := r.handle.WithContext(ctx).Find(&got).Error; !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a read under a refused entry gave %v (%d rows), want ErrNoTenantInContext",
					err, len(got))
			}
		})
	}
}

func TestEnterTellsAnUnknownTenantFromADisabledOne(t *testing.T) {
	r := enteringRoster(t)
	_, unknown := r.Enter(context.Background(), "t_ghost")
	_, disabled := r.Enter(context.Background(), "t_paused")
	if errors.Is(unknown, tenant.ErrTenantDisabled) || errors.Is(disabled, tenant.ErrInvalidTenant) {
		t.Fatalf("the refusals are not told apart: unknown %v, disabled %v", unknown, disabled)
	}
}

func TestEnterRefusesADeletedTenantAsItsOwnRefusal(t *testing.T) {
	r := enteringRoster(t)
	_, unknown := r.Enter(context.Background(), "t_ghost")
	_, disabled := r.Enter(context.Background(), "t_paused")
	_, deleted := r.Enter(context.Background(), "t_deleted")
	if !errors.Is(deleted, tenant.ErrTenantDeleted) {
		t.Fatalf("entering a deleted tenant gave %v, want ErrTenantDeleted", deleted)
	}
	if errors.Is(deleted, tenant.ErrTenantDisabled) || errors.Is(deleted, tenant.ErrInvalidTenant) {
		t.Errorf("a deleted tenant is reported as disabled or unknown too: %v", deleted)
	}
	if errors.Is(unknown, tenant.ErrTenantDeleted) || errors.Is(disabled, tenant.ErrTenantDeleted) {
		t.Errorf("an unknown or disabled tenant is reported as deleted: unknown %v, disabled %v", unknown, disabled)
	}
}

func TestEnterRegisteredEntersADeletedTenant(t *testing.T) {
	r := enteringRoster(t)
	if err := r.handle.WithContext(crossContext()).Exec(
		"INSERT INTO orders (tenant_id, id, note) VALUES ('t_deleted', 'd', 'z')").Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}
	ctx, err := r.EnterRegistered(crossContext(), "t_deleted")
	if err != nil {
		t.Fatalf("entering a deleted tenant through EnterRegistered gave %v, want the tenant", err)
	}
	requireScopedTo(t, r, ctx, "t_deleted")
}

func TestEnterBeforeTheRosterIsReadRefuses(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if _, err := r.Enter(context.Background(), "t_acme"); !errors.Is(err, tenant.ErrRosterMissing) {
		t.Fatalf("entering before the roster was read gave %v, want ErrRosterMissing", err)
	}
}

func TestEnterSeesADisableOnTheWritersReplica(t *testing.T) {
	r := enteringRoster(t)
	if err := r.SetStatus(context.Background(), "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling: %v", err)
	}
	if _, err := r.Enter(context.Background(), "t_acme"); !errors.Is(err, tenant.ErrTenantDisabled) {
		t.Fatalf("entering a tenant just disabled gave %v, want ErrTenantDisabled", err)
	}
}

func TestEnterAdmitsWhatAnotherReplicaWroteSinceTheLastRead(t *testing.T) {
	for name, write := range map[string]func(ctx context.Context, other *roster) error{
		"registered": func(ctx context.Context, other *roster) error {
			return other.Create(ctx, tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"served again": func(ctx context.Context, other *roster) error {
			return other.SetStatus(ctx, "t_new", tenant.StatusActive)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := enteringRoster(t)
			if name == "served again" {
				if err := r.handle.Exec(
					"INSERT INTO tenants (id, name, status) VALUES ('t_new', 'new', 0)").Error; err != nil {
					t.Fatalf("seeding: %v", err)
				}
				if err := r.read(context.Background()); err != nil {
					t.Fatalf("reading: %v", err)
				}
			}
			if err := write(context.Background(), secondReplica(t, r)); err != nil {
				t.Fatalf("writing on the other replica: %v", err)
			}

			ctx, err := r.Enter(context.Background(), "t_new")
			if err != nil {
				t.Fatalf("entering a tenant the table serves gave %v, want the tenant", err)
			}
			if id, ok := tenant.FromContext(ctx); !ok || id != "t_new" {
				t.Fatalf("the entered context reports tenant (%q, %v), want t_new", id, ok)
			}
			got, ok, err := r.ByName(context.Background(), "new")
			if err != nil || !ok || got.Status != tenant.StatusActive {
				t.Fatalf("this replica's snapshot answers (%+v, %v, %v), want the tenant the table serves",
					got, ok, err)
			}
		})
	}
}

func TestEnterRefusesWhatTheTableRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		row  string
		want error
	}{
		{
			"registered disabled", "INSERT INTO tenants (id, name, status) VALUES ('t_new', 'new', 0)",
			tenant.ErrTenantDisabled,
		},
		{
			"outside the domain", "INSERT INTO tenants (id, name, status) VALUES ('t_new', 'new name', 1)",
			tenant.ErrInvalidTenant,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := enteringRoster(t)
			if err := r.handle.Exec(c.row).Error; err != nil {
				t.Fatalf("writing the row: %v", err)
			}
			ctx, err := r.Enter(crossContext(), "t_new")
			if !errors.Is(err, c.want) {
				t.Fatalf("entering gave %v, want %v", err, c.want)
			}
			if id, ok := tenant.FromContext(ctx); ok || tenantctx.CrossTenant(ctx) {
				t.Fatalf("a refused entry handed back tenant (%q, %v), crossing %v; want neither",
					id, ok, tenantctx.CrossTenant(ctx))
			}
		})
	}
}

func TestEnterOverADroppedTableGivesNoAnswer(t *testing.T) {
	for name, enter := range map[string]func(r *roster) (context.Context, error){
		"Enter": func(r *roster) (context.Context, error) { return r.Enter(context.Background(), "t_new") },
		"Enter a disabled tenant": func(r *roster) (context.Context, error) {
			return r.Enter(context.Background(), "t_paused")
		},
		"EnterRegistered": func(r *roster) (context.Context, error) {
			return r.EnterRegistered(context.Background(), "t_new")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := enteringRoster(t)
			if err := r.handle.Exec("DROP TABLE tenants").Error; err != nil {
				t.Fatalf("dropping the table: %v", err)
			}
			ctx, err := enter(r)
			if !errors.Is(err, tenant.ErrRosterMissing) ||
				errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDisabled) {
				t.Fatalf("entering over a dropped table gave %v, want ErrRosterMissing and neither refusal", err)
			}
			if id, ok := tenant.FromContext(ctx); ok || tenantctx.CrossTenant(ctx) {
				t.Fatalf("a failed entry handed back tenant (%q, %v), crossing %v; want neither",
					id, ok, tenantctx.CrossTenant(ctx))
			}
		})
	}
}

func TestAWriteOverADroppedTableReportsTheRosterMissing(t *testing.T) {
	for name, write := range map[string]func(r *roster) error{
		"Put": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"Create": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive})
		},
		"SetStatus": func(r *roster) error {
			return r.SetStatus(context.Background(), "t_acme", tenant.StatusDisabled)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := enteringRoster(t)
			if err := r.handle.Exec("DROP TABLE tenants").Error; err != nil {
				t.Fatalf("dropping the table: %v", err)
			}
			if err := write(r); !errors.Is(err, tenant.ErrRosterMissing) {
				t.Fatalf("%s over a dropped table gave %v, want ErrRosterMissing", name, err)
			}
		})
	}
}

func countFullReads(t *testing.T, r *roster) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	err := r.handle.Callback().Query().Before("gorm:query").Register("test:count-full-reads",
		func(tx *gorm.DB) {
			if _, conditioned := tx.Statement.Clauses["WHERE"]; !conditioned && tx.Statement.Table == tableName {
				n.Add(1)
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
	return &n
}

func rowReadsArriveTogether(t *testing.T, r *roster, n int) {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	err := r.handle.Callback().Query().After("gorm:query").Register("test:row-reads-arrive-together",
		func(tx *gorm.DB) {
			if _, conditioned := tx.Statement.Clauses["WHERE"]; !conditioned || tx.Statement.Table != tableName {
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

func TestEntriesTogetherForATenantTheSnapshotLacksRereadTheRosterOnce(t *testing.T) {
	r := enteringRoster(t)
	if err := secondReplica(t, r).Create(context.Background(),
		tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("registering on the other replica: %v", err)
	}
	reads := countFullReads(t, r)

	const entries = 8
	rowReadsArriveTogether(t, r, entries)
	var first atomic.Bool
	if err := r.handle.Callback().Query().After("gorm:query").Register("test:slow-first-full-read",
		func(tx *gorm.DB) {
			if _, conditioned := tx.Statement.Clauses["WHERE"]; !conditioned && tx.Statement.Table == tableName &&
				first.CompareAndSwap(false, true) {
				time.Sleep(50 * time.Millisecond)
			}
		}); err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, entries)
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.Enter(context.Background(), "t_new")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("entry %d gave %v, want the tenant", i, err)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("the entries read the whole roster %d times, want once", got)
	}
}

func TestEnterRegisteredScopesAPathToADisabledTenant(t *testing.T) {
	r := enteringRoster(t)
	if err := r.handle.WithContext(crossContext()).Exec(
		"INSERT INTO orders (tenant_id, id, note) VALUES ('t_paused', 'p', 'z')").Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}
	base, rec := loggedContext()
	ctx, err := r.EnterRegistered(base, "t_paused")
	if err != nil {
		t.Fatalf("entering a disabled tenant: %v", err)
	}
	log.FromContext(ctx).Info("working")
	requireLogged(t, rec, "t_paused")
	requireScopedTo(t, r, ctx, "t_paused")
}

func TestEnterRegisteredEntersAServedTenant(t *testing.T) {
	r := enteringRoster(t)
	ctx, err := r.EnterRegistered(crossContext(), "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	requireScopedTo(t, r, ctx, "t_acme")
}

func TestEnterRegisteredRefusesAnIdentifierNoRowCarries(t *testing.T) {
	for name, id := range map[string]tenant.ID{
		"no row carries it": "t_ghost", "empty": "", "outside the domain": "t acme",
	} {
		t.Run(name, func(t *testing.T) {
			r := enteringRoster(t)
			ctx, err := r.EnterRegistered(crossContext(), id)
			if !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("entering %q gave %v, want ErrInvalidTenant", id, err)
			}
			var got []order
			if err := r.handle.WithContext(ctx).Find(&got).Error; !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a read under a refused entry gave %v (%d rows), want ErrNoTenantInContext",
					err, len(got))
			}
		})
	}
}

func TestEnterRegisteredAdmitsATenantAnotherReplicaRegistered(t *testing.T) {
	r := enteringRoster(t)
	if err := secondReplica(t, r).Create(context.Background(),
		tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusDisabled}); err != nil {
		t.Fatalf("registering on the other replica: %v", err)
	}
	ctx, err := r.EnterRegistered(context.Background(), "t_new")
	if err != nil {
		t.Fatalf("entering a tenant the table holds gave %v, want the tenant", err)
	}
	if id, ok := tenant.FromContext(ctx); !ok || id != "t_new" {
		t.Fatalf("the entered context reports tenant (%q, %v), want t_new", id, ok)
	}
}

type fieldRecorder struct {
	mu      *sync.Mutex
	records *[][]slog.Attr
	bound   []slog.Attr
}

func newFieldRecorder() *fieldRecorder {
	return &fieldRecorder{mu: &sync.Mutex{}, records: &[][]slog.Attr{}}
}

func (f *fieldRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (f *fieldRecorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := append([]slog.Attr(nil), f.bound...)
	rec.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.records = append(*f.records, attrs)
	return nil
}

func (f *fieldRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &fieldRecorder{mu: f.mu, records: f.records, bound: append(append([]slog.Attr(nil), f.bound...), attrs...)}
}

func (f *fieldRecorder) WithGroup(string) slog.Handler { return f }

func (f *fieldRecorder) values(t *testing.T, key string) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(*f.records) != 1 {
		t.Fatalf("the logger holds %d records, want one", len(*f.records))
	}
	var out []string
	for _, a := range (*f.records)[0] {
		if a.Key == key {
			out = append(out, a.Value.String())
		}
	}
	return out
}

func loggedContext() (context.Context, *fieldRecorder) {
	rec := newFieldRecorder()
	ctx := log.WithLogger(context.Background(), slog.New(rec))
	return log.WithAttrs(ctx, slog.String("job", "reindex")), rec
}

func requireLogged(t *testing.T, rec *fieldRecorder, want ...string) {
	t.Helper()
	if got := rec.values(t, tenantctx.LogField); !slices.Equal(got, want) {
		t.Fatalf("the record carries %s %q, want %q", tenantctx.LogField, got, want)
	}
	if got := rec.values(t, "job"); !slices.Equal(got, []string{"reindex"}) {
		t.Fatalf("the record carries job %q, want the one bound outside", got)
	}
}

func TestEnterBindsTheTenantOnTheLogger(t *testing.T) {
	r := enteringRoster(t)
	base, rec := loggedContext()
	ctx, err := r.Enter(base, "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	log.FromContext(ctx).Info("working")
	requireLogged(t, rec, "t_acme")
}

func TestEnterReplacesTheTenantOnTheLogger(t *testing.T) {
	r := enteringRoster(t)
	base, rec := loggedContext()
	first, err := r.Enter(base, "t_globex")
	if err != nil {
		t.Fatalf("entering the first tenant: %v", err)
	}
	ctx, err := r.Enter(first, "t_acme")
	if err != nil {
		t.Fatalf("entering the second tenant: %v", err)
	}
	log.FromContext(ctx).Info("working")
	requireLogged(t, rec, "t_acme")
}

func TestARefusedEnterTakesTheTenantOffTheLogger(t *testing.T) {
	r := enteringRoster(t)
	base, rec := loggedContext()
	entered, err := r.Enter(base, "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	refused, err := r.Enter(entered, "t_ghost")
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("entering a tenant no row carries gave %v, want ErrInvalidTenant", err)
	}
	log.FromContext(refused).Info("working")
	requireLogged(t, rec)
}

func TestCrossingFromAnEnteredContextTakesTheTenantOffTheLogger(t *testing.T) {
	r := enteringRoster(t)
	base, rec := loggedContext()
	entered, err := r.Enter(base, "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	log.FromContext(tenant.CrossTenant(entered)).Info("working")
	requireLogged(t, rec)
}
