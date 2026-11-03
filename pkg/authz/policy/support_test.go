package policy

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
)

// applyMigration runs one of this module's own migration files, so the tables a
// case writes into are the ones the module ships rather than a copy written out
// in the test.
func applyMigration(t *testing.T, handle *gorm.DB, name string) {
	t.Helper()
	sql, err := fs.ReadFile(migrationsFS, name)
	if err != nil {
		t.Fatalf("reading the migration %s: %v", name, err)
	}
	for statement := range strings.SplitSeq(string(sql), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
}

// newProductOver builds a product over a real SQLite handle with the tenant
// filter installed and the policy tables created from the module's own
// migration. Nothing is wired and nothing is started: a case sets the optional
// capabilities it needs and calls begin or wire itself.
func newProductOver(t *testing.T) *product {
	t.Helper()
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil).DB()
	if err := handle.Use(tenantFilter(t)); err != nil {
		t.Fatalf("installing the tenant filter: %v", err)
	}
	applyMigration(t, handle, "sqlite/0001_authz_policy.sql")
	reg := core.New()
	reg.Register(Module())
	return &product{
		handle:      handle,
		dialect:     db.SQLite,
		text:        authz.ModelText,
		loadTimeout: 5 * time.Second,
		catalog:     mustBuildCatalog(t, reg),
		logger:      log.Default(),
		tenantLock:  authz.NewInProcessTenantLock(),
	}
}

// mustBuildCatalog builds the catalog from a registry and fails the case on a
// declaration the catalog refuses.
func mustBuildCatalog(t *testing.T, reg *core.Registry) *catalog {
	t.Helper()
	c, err := buildCatalog(reg)
	if err != nil {
		t.Fatalf("building the catalog: %v", err)
	}
	return c
}

// wireProduct supplies the given optional capabilities to a registry and wires
// the product against it, so that the subscription reads them the way an
// assembly delivers them.
func wireProduct(t *testing.T, p *product, bus event.Bus) {
	t.Helper()
	reg := core.New()
	if bus != nil {
		reg.Register(core.Module{Name: "stand-in.bus", Provides: []core.Provision{{Token: (*event.Bus)(nil)}}})
		reg.Supply(bus, "stand-in.bus")
	}
	if err := p.wire(reg); err != nil {
		t.Fatalf("wiring the product: %v", err)
	}
}

// allowed asks the product whether an account may act, failing the case on an
// error rather than reading it as a denial.
func allowed(t *testing.T, ctx context.Context, p *product, account, domain, object, action string) bool {
	t.Helper()
	ok, err := p.Allowed(ctx, authz.Query{
		Subject: account, Domain: authz.Domain(domain), Object: object, Action: action,
	})
	if err != nil {
		t.Fatalf("Allowed(%s, %s, %s, %s): %v", account, domain, object, action, err)
	}
	return ok
}

// seedPolicy writes one role, its grant and a binding straight into the tables
// under the cross-tenant mark, bypassing the manager so that a case can tell a
// reload apart from the write that would have carried it.
func seedPolicy(t *testing.T, p *product, tenant, role, account, object, action string) {
	t.Helper()
	now := time.Now().UTC()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{
			"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES (?,?,?,?,?)",
			[]any{tenant, role, false, "k", now},
		},
		{
			"INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES (?,?,?,?,?)",
			[]any{tenant, role, object, action, now},
		},
		{
			"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
			[]any{tenant, account, role, now},
		},
	} {
		if err := p.crossing(t.Context()).Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatalf("seeding the policy: %v", err)
		}
	}
}

// pollUntil reports whether cond became true within the bound. It polls rather
// than sleeps a fixed time, so the case ends the moment the condition holds.
func pollUntil(t *testing.T, within time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// recordingBus is an event bus that keeps what it was given and lets a case
// drive its handlers directly. It stands in for the memory module's bus, whose
// constructor is unexported: assembling that module through a registry would
// turn each case into an integration test.
type recordingBus struct {
	mu        sync.Mutex
	published []event.Event
	handlers  map[string][]event.Handler
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string][]event.Handler)}
}

func (b *recordingBus) Publish(ctx context.Context, evt event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, evt)
	return nil
}

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) events() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

// deliver runs every handler subscribed to a type, the way a backend would.
func (b *recordingBus) deliver(t *testing.T, eventType string, evt event.Event) {
	t.Helper()
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.handlers[eventType]...)
	b.mu.Unlock()
	for _, h := range handlers {
		if err := h(context.Background(), evt); err != nil {
			t.Fatalf("the subscription handler failed: %v", err)
		}
	}
}
