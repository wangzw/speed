package persist

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	casbinmodel "github.com/casbin/casbin/v3/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

// dialect is one database the adapter is exercised on: how to open a handle of
// it, and the policy tables' DDL for it. The pairs differ only in the timestamp
// type, as the migrations do.
type dialect struct {
	name string
	open func(t *testing.T) *gorm.DB
	ddl  []string
}

var sqliteDDL = []string{
	`CREATE TABLE authz_roles (
		tenant_id       TEXT NOT NULL CHECK (tenant_id <> ''),
		role            TEXT NOT NULL,
		builtin         BOOLEAN NOT NULL,
		description_key TEXT NOT NULL,
		created_at      DATETIME NOT NULL,
		PRIMARY KEY (tenant_id, role))`,
	`CREATE TABLE authz_grants (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		role       TEXT NOT NULL,
		resource   TEXT NOT NULL,
		action     TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY (tenant_id, role, resource, action))`,
	`CREATE INDEX authz_grants_by_object ON authz_grants (tenant_id, resource, action)`,
	`CREATE TABLE authz_bindings (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		account_id TEXT NOT NULL,
		role       TEXT NOT NULL,
		granted_at DATETIME NOT NULL,
		PRIMARY KEY (tenant_id, account_id, role))`,
	`CREATE INDEX authz_bindings_by_role ON authz_bindings (tenant_id, role)`,
}

var postgresDDL = []string{
	`CREATE TABLE authz_roles (
		tenant_id       TEXT NOT NULL CHECK (tenant_id <> ''),
		role            TEXT NOT NULL,
		builtin         BOOLEAN NOT NULL,
		description_key TEXT NOT NULL,
		created_at      TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, role))`,
	`CREATE TABLE authz_grants (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		role       TEXT NOT NULL,
		resource   TEXT NOT NULL,
		action     TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, role, resource, action))`,
	`CREATE INDEX authz_grants_by_object ON authz_grants (tenant_id, resource, action)`,
	`CREATE TABLE authz_bindings (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		account_id TEXT NOT NULL,
		role       TEXT NOT NULL,
		granted_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, account_id, role))`,
	`CREATE INDEX authz_bindings_by_role ON authz_bindings (tenant_id, role)`,
}

// sqliteSeq keeps each in-memory database of its own, so two handles opened in
// one case do not share one by name.
var sqliteSeq atomic.Int64

func sqliteDialect() dialect {
	return dialect{
		name: "sqlite",
		open: func(t *testing.T) *gorm.DB {
			t.Helper()
			dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), sqliteSeq.Add(1))
			handle, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
			if err != nil {
				t.Fatalf("opening the SQLite handle: %v", err)
			}
			closePool(t, handle)
			return handle
		},
		ddl: sqliteDDL,
	}
}

func postgresDialect() dialect {
	return dialect{
		name: "postgres",
		open: func(t *testing.T) *gorm.DB {
			t.Helper()
			handle, err := gorm.Open(postgres.Open(pgtest.Acquire(t)), &gorm.Config{})
			if err != nil {
				t.Fatalf("opening the PostgreSQL handle: %v", err)
			}
			closePool(t, handle)
			return handle
		},
		ddl: postgresDDL,
	}
}

// onEachDialect runs one case on every database the adapter is exercised on.
// The PostgreSQL half skips itself where the machine cannot run it.
func onEachDialect(t *testing.T, run func(t *testing.T, d dialect)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { run(t, sqliteDialect()) })
	t.Run("postgres", func(t *testing.T) { run(t, postgresDialect()) })
}

func closePool(t *testing.T, handle *gorm.DB) {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
}

// tenantFilter returns the real statement filter the tenant catalog has
// installed on the handles the database module opens, taken from the module's
// own declarations. Using it here is what makes the load case discriminate:
// without the filter installed, the cross-tenant mark would change nothing.
func tenantFilter(t *testing.T) gorm.Plugin {
	t.Helper()
	for _, resource := range catalog.Module().Resources {
		declared, ok := resource.(db.Plugin)
		if ok && declared.Plugin != nil {
			return declared.Plugin
		}
	}
	t.Fatal("the tenant catalog declares no statement filter")
	return nil
}

// open builds a handle with the policy tables created, and the tenant filter
// installed unless withoutFilter is set.
func open(t *testing.T, d dialect, withoutFilter bool) *gorm.DB {
	t.Helper()
	handle := d.open(t)
	if !withoutFilter {
		if err := handle.Use(tenantFilter(t)); err != nil {
			t.Fatalf("installing the tenant filter: %v", err)
		}
	}
	for _, statement := range d.ddl {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating a policy table: %v", err)
		}
	}
	return handle
}

// crossing is the context the adapter's own statements carry, for a case that
// reads or writes the tables directly.
func crossing() context.Context { return tenant.CrossTenant(context.Background()) }

// seed writes two tenants' grants and bindings around the model, so both a
// load and a filtered read have more than one tenant to tell apart.
func seed(t *testing.T, handle *gorm.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES
			('acme', 'roleA', 'account', 'read', '2020-01-01T00:00:00Z'),
			('globex', 'roleG', 'account', 'read', '2020-01-01T00:00:00Z')`,
		`INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES
			('acme', 'acc1', 'roleA', '2020-01-01T00:00:00Z'),
			('globex', 'acc2', 'roleG', '2020-01-01T00:00:00Z')`,
	}
	for _, statement := range statements {
		if err := handle.WithContext(crossing()).Exec(statement).Error; err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

// policies renders every rule a model holds as "sec field field …", for a case
// to compare a whole load at once.
func policies(m casbinmodel.Model) []string {
	var out []string
	for _, sec := range []string{"p", "g"} {
		for _, ast := range m[sec] {
			for _, rule := range ast.Policy {
				out = append(out, sec+" "+strings.Join(rule, " "))
			}
		}
	}
	sort.Strings(out)
	return out
}

func count(t *testing.T, handle *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := handle.Raw("SELECT count(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// A load reads every tenant's rows, because the deciding process holds all of
// them; the cross-tenant mark the adapter binds at construction is what lets it.
// The control half shows the mark is load-bearing: the same read with no mark
// and no tenant is refused, not answered with all rows.
func TestLoadPolicyReadsEveryTenant(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		m, err := casbinmodel.NewModelFromString(authz.ModelText)
		if err != nil {
			t.Fatalf("building the model: %v", err)
		}
		err = New(t.Context(), handle, "").LoadPolicy(m)
		if err != nil {
			t.Fatalf("loading the policy: %v", err)
		}

		want := []string{
			"g acc1 roleA acme",
			"g acc2 roleG globex",
			"p roleA acme account read",
			"p roleG globex account read",
		}
		if got := policies(m); !equal(got, want) {
			t.Errorf("the loaded policy is %v, want both tenants' rows %v", got, want)
		}

		// The control: the same read without the mark is refused rather than
		// reading across tenants.
		var rows []grantRow
		err = handle.WithContext(context.Background()).Find(&rows).Error
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("a read with no tenant and no cross-tenant mark gave %v, want a refusal", err)
		}
	})
}

// A rule whose domain is not the caller's is refused before any statement is
// issued, and the same rule through the tenant's own adapter is written — so
// the refusal is the tenant check and not a constraint of the schema.
func TestAWriteForAnotherTenantIsRefused(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)

		acme := New(t.Context(), handle, "acme")
		err := acme.AddPolicy("p", "p", []string{"roleG", "globex", "account", "read"})
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a p rule for globex through the acme adapter gave %v, want ErrTenantMismatch", err)
		}
		err = acme.AddPolicy("g", "g", []string{"acc2", "roleG", "globex"})
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a g rule for globex through the acme adapter gave %v, want ErrTenantMismatch", err)
		}
		if n := count(t, handle, "authz_grants") + count(t, handle, "authz_bindings"); n != 0 {
			t.Errorf("the refused writes left %d row(s), want none", n)
		}

		globex := New(t.Context(), handle, "globex")
		if err := globex.AddPolicy("p", "p", []string{"roleG", "globex", "account", "read"}); err != nil {
			t.Fatalf("the same p rule through the globex adapter: %v", err)
		}
		if err := globex.AddPolicy("g", "g", []string{"acc2", "roleG", "globex"}); err != nil {
			t.Fatalf("the same g rule through the globex adapter: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s), want the one globex rule", n)
		}
		if n := count(t, handle, "authz_bindings"); n != 1 {
			t.Errorf("the binding table holds %d row(s), want the one globex rule", n)
		}
	})
}

// SavePolicy is not implemented: the adapter returns its own sentinel.
func TestSavePolicyIsNotImplemented(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		m, err := casbinmodel.NewModelFromString(authz.ModelText)
		if err != nil {
			t.Fatalf("building the model: %v", err)
		}
		if err := New(t.Context(), handle, "acme").SavePolicy(m); !errors.Is(err, ErrNotImplemented) {
			t.Errorf("SavePolicy gave %v, want ErrNotImplemented", err)
		}
	})
}

// The two rules map onto the two tables field by field, and the mapping reads
// back the same way.
func TestRulesMapToColumns(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		adapter := New(t.Context(), handle, "acme")
		if err := adapter.AddPolicy("p", "p", []string{"roleA", "acme", "account", "read"}); err != nil {
			t.Fatalf("writing the p rule: %v", err)
		}
		if err := adapter.AddPolicy("g", "g", []string{"acc1", "roleA", "acme"}); err != nil {
			t.Fatalf("writing the g rule: %v", err)
		}

		var grant struct {
			TenantID  string
			Role      string
			Resource  string
			Action    string
			CreatedAt time.Time
		}
		if err := handle.Raw("SELECT tenant_id, role, resource, action, created_at FROM authz_grants").
			Scan(&grant).Error; err != nil {
			t.Fatalf("reading the grant row: %v", err)
		}
		wantGrant := struct {
			TenantID, Role, Resource, Action string
		}{"acme", "roleA", "account", "read"}
		if grant.TenantID != wantGrant.TenantID || grant.Role != wantGrant.Role ||
			grant.Resource != wantGrant.Resource || grant.Action != wantGrant.Action {
			t.Errorf("the grant row maps to %+v, want %+v", grant, wantGrant)
		}
		if grant.CreatedAt.IsZero() {
			t.Error("the grant row was written without a created_at")
		}

		var binding struct {
			TenantID  string
			AccountID string
			Role      string
			GrantedAt time.Time
		}
		if err := handle.Raw("SELECT tenant_id, account_id, role, granted_at FROM authz_bindings").
			Scan(&binding).Error; err != nil {
			t.Fatalf("reading the binding row: %v", err)
		}
		wantBinding := struct {
			TenantID, AccountID, Role string
		}{"acme", "acc1", "roleA"}
		if binding.TenantID != wantBinding.TenantID || binding.AccountID != wantBinding.AccountID ||
			binding.Role != wantBinding.Role {
			t.Errorf("the binding row maps to %+v, want %+v", binding, wantBinding)
		}
		if binding.GrantedAt.IsZero() {
			t.Error("the binding row was written without a granted_at")
		}

		// The reverse: the rows read back into the rule shapes.
		m, err := casbinmodel.NewModelFromString(authz.ModelText)
		if err != nil {
			t.Fatalf("building the model: %v", err)
		}
		if err := New(t.Context(), handle, "").LoadPolicy(m); err != nil {
			t.Fatalf("loading the policy: %v", err)
		}
		want := []string{"g acc1 roleA acme", "p roleA acme account read"}
		if got := policies(m); !equal(got, want) {
			t.Errorf("the rows load back as %v, want the rule shapes %v", got, want)
		}
	})
}

// A rule with no tenant is refused three ways over: the filter refuses the write
// when it is installed, the identifier refuses the empty value to be bound where
// no filter is, and the table's CHECK refuses a row written around the model.
func TestAnEmptyTenantIsRefused(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		// First, no filter: the empty identifier is refused at the binding, and
		// the CHECK refuses a row written as raw SQL.
		plain := open(t, d, true)
		err := New(t.Context(), plain, "").AddPolicy("p", "p", []string{"roleA", "", "account", "read"})
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("writing a rule with no tenant and no filter gave %v, want ErrNoTenantInContext", err)
		}
		err = plain.Exec(`INSERT INTO authz_grants (tenant_id, role, resource, action, created_at)
			VALUES ('', 'roleA', 'account', 'read', '2020-01-01T00:00:00Z')`).Error
		if err == nil {
			t.Error("the table accepted a row whose tenant column is empty")
		}

		// Then with the filter: it refuses the same write ahead of the database.
		filtered := open(t, d, false)
		err = New(t.Context(), filtered, "").AddPolicy("p", "p", []string{"roleA", "", "account", "read"})
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("writing a rule with no tenant under the filter gave %v, want ErrNoTenantInContext", err)
		}
		if n := count(t, filtered, "authz_grants"); n != 0 {
			t.Errorf("the refused write left %d row(s), want none", n)
		}
	})
}

// A filtered removal reaches only the caller's rows, and a filter naming
// another tenant's domain is refused rather than run.
func TestRemoveFilteredPolicyIsHeldToTheTenant(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		// Field 1 of a p rule is its domain: naming globex through the acme
		// adapter is refused.
		err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("p", "p", 1, "globex")
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a filtered removal naming globex gave %v, want ErrTenantMismatch", err)
		}
		if n := count(t, handle, "authz_grants"); n != 2 {
			t.Errorf("the refused removal left %d row(s), want both", n)
		}

		// Field 2 is the resource, and both tenants hold a grant on "account":
		// the removal takes acme's matching grant and leaves globex's, so a
		// removal that crossed tenants would be seen.
		if err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("p", "p", 2, "account"); err != nil {
			t.Fatalf("removing acme's account grants: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s), want globex's alone", n)
		}
		if n := count(t, handle, "authz_bindings"); n != 2 {
			t.Errorf("the binding table holds %d row(s), want it untouched", n)
		}
	})
}

// A removal addressed by a rule's whole key removes exactly that tenant's row.
func TestRemovePolicyTouchesOneRow(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		if err := New(t.Context(), handle, "acme").RemovePolicy("p", "p", []string{"roleA", "acme", "account", "read"}); err != nil {
			t.Fatalf("removing the acme rule: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s) after the removal, want the other tenant's", n)
		}
		if n := count(t, handle, "authz_bindings"); n != 2 {
			t.Errorf("the binding table holds %d row(s), want it untouched", n)
		}
	})
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
