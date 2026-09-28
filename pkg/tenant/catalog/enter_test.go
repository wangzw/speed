package catalog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// The cases in this file are about entering one tenant from a path with no
// request behind it: what the roster has to say first, and what the entered
// context then does to the statements issued under it.

// enteringRoster is a roster that has read two tenants — one served, one
// disabled — over a handle with the filter installed and an orders table
// holding a row for each of two tenants.
func enteringRoster(t *testing.T) *roster {
	t.Helper()
	r := rosterOver(t,
		insert("t_acme", "acme"),
		insert("t_globex", "globex"),
		"INSERT INTO tenants (id, alias, status) VALUES ('t_paused', 'paused', 0)")
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

// requireScopedTo asserts that statements under ctx read and write this
// tenant's rows and no other's.
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

// A path that crossed tenants and then enters one is in that tenant: the mark
// does not survive into the entered context.
func TestEnterFromACrossingContextLiftsTheMark(t *testing.T) {
	r := enteringRoster(t)
	ctx, err := r.Enter(crossContext(), "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	requireScopedTo(t, r, ctx, "t_acme")
}

// An event handed over with the publisher's context carries the publisher's
// tenant; entering the tenant the event names replaces it.
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
			// The context handed back with the refusal is no tenant's and
			// does not cross: a caller that drops the error is refused.
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

// The two refusals are told apart: a caller drops work for a tenant that does
// not exist and waits out one that is disabled.
func TestEnterTellsAnUnknownTenantFromADisabledOne(t *testing.T) {
	r := enteringRoster(t)
	_, unknown := r.Enter(context.Background(), "t_ghost")
	_, disabled := r.Enter(context.Background(), "t_paused")
	if errors.Is(unknown, tenant.ErrTenantDisabled) || errors.Is(disabled, tenant.ErrInvalidTenant) {
		t.Fatalf("the refusals are not told apart: unknown %v, disabled %v", unknown, disabled)
	}
}

func TestEnterBeforeTheRosterIsReadRefuses(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if _, err := r.Enter(context.Background(), "t_acme"); !errors.Is(err, tenant.ErrRosterMissing) {
		t.Fatalf("entering before the roster was read gave %v, want ErrRosterMissing", err)
	}
}

// The writer's replica sees its own write, so a tenant disabled here cannot be
// entered here from the next call on.
func TestEnterSeesADisableOnTheWritersReplica(t *testing.T) {
	r := enteringRoster(t)
	if err := r.SetStatus(context.Background(), "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling: %v", err)
	}
	if _, err := r.Enter(context.Background(), "t_acme"); !errors.Is(err, tenant.ErrTenantDisabled) {
		t.Fatalf("entering a tenant just disabled gave %v, want ErrTenantDisabled", err)
	}
}

// fieldRecorder is a slog handler that keeps the attributes bound to it and
// passed with each record.
type fieldRecorder struct {
	mu     *sync.Mutex
	fields *[]map[string]string
	bound  []slog.Attr
}

func (f *fieldRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (f *fieldRecorder) Handle(_ context.Context, rec slog.Record) error {
	m := map[string]string{}
	for _, a := range f.bound {
		m[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()
		return true
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.fields = append(*f.fields, m)
	return nil
}

func (f *fieldRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &fieldRecorder{mu: f.mu, fields: f.fields, bound: append(append([]slog.Attr(nil), f.bound...), attrs...)}
}

func (f *fieldRecorder) WithGroup(string) slog.Handler { return f }

// A record written under the entered context is attributed to the tenant, the
// way a record written under a resolved request is, beside what was bound
// before.
func TestEnterBindsTheTenantOnTheLogger(t *testing.T) {
	r := enteringRoster(t)
	fields := &[]map[string]string{}
	base := log.WithLogger(context.Background(), slog.New(&fieldRecorder{mu: &sync.Mutex{}, fields: fields}))
	base = log.WithAttrs(base, slog.String("job", "reindex"))
	ctx, err := r.Enter(base, "t_acme")
	if err != nil {
		t.Fatalf("entering: %v", err)
	}
	log.FromContext(ctx).Info("working")
	if len(*fields) != 1 || (*fields)[0][tenantctx.LogField] != "t_acme" || (*fields)[0]["job"] != "reindex" {
		t.Fatalf("a record under the entered context carried %v, want %s=t_acme beside job=reindex",
			*fields, tenantctx.LogField)
	}
}
