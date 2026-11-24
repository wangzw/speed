package catalog

import (
	"context"
	"errors"
	"testing"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

type order struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (order) TableName() string { return "orders" }

type memo struct {
	ID       string `gorm:"column:id;primaryKey"`
	TenantID string `gorm:"column:tenant_id;not null"`
	Note     string `gorm:"column:note"`
}

func (memo) TableName() string { return "memos" }

// open builds a handle with the filter installed and closes its pool when the case
// ends: the in-memory database is shared by name and would otherwise survive into
// the next run under -count.
func open(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{})
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	if err := handle.Use(newFilter()); err != nil {
		t.Fatalf("installing the filter: %v", err)
	}
	for _, ddl := range []string{
		"CREATE TABLE orders (tenant_id TEXT NOT NULL, id TEXT NOT NULL, note TEXT, PRIMARY KEY (tenant_id, id))",
		"CREATE TABLE memos (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, note TEXT)",
	} {
		if err := handle.Exec(ddl).Error; err != nil {
			t.Fatalf("creating a table: %v", err)
		}
	}
	return handle
}

func seed(t *testing.T, handle *gorm.DB) {
	t.Helper()
	ctx := tenant.CrossTenant(context.Background())
	if err := handle.WithContext(ctx).Exec(
		"INSERT INTO orders (id, tenant_id, note) VALUES ('a', 'acme', 'for acme'), " +
			"('b', 'globex', 'for globex')").Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}
}

func scopedContext(id string) context.Context {
	return tenantctx.WithID(context.Background(), id)
}

// TestReadIsScopedToTheContext checks that a read returns only the context
// tenant's rows.
func TestReadIsScopedToTheContext(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []order
	if err := handle.WithContext(scopedContext("acme")).Find(&got).Error; err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("a scoped read returned %d rows (%v), want just acme's", len(got), got)
	}
}

// TestReadWithoutATenantIsRefused checks that a read with no tenant in the
// context fails with ErrNoTenantInContext.
func TestReadWithoutATenantIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []order
	err := handle.WithContext(context.Background()).Find(&got).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("reading with no tenant gave %v, want ErrNoTenantInContext", err)
	}
	if len(got) != 0 {
		t.Fatalf("a refused read still returned %d rows", len(got))
	}
}

// TestCrossTenantReadsEverything checks that a cross-tenant read returns every
// tenant's rows.
func TestCrossTenantReadsEverything(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []order
	err := handle.WithContext(tenant.CrossTenant(context.Background())).Find(&got).Error
	if err != nil {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("a cross-tenant read returned %d rows, want both tenants'", len(got))
	}
}

// TestUnscopedModelIsUntouched checks that a model that does not embed the
// convention is read without any tenant in the context.
func TestUnscopedModelIsUntouched(t *testing.T) {
	handle := open(t)
	if err := handle.Exec(
		"INSERT INTO memos (id, tenant_id, note) VALUES ('m', 'whatever', 'unscoped')").
		Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}

	var got []memo
	if err := handle.WithContext(context.Background()).Find(&got).Error; err != nil {
		t.Fatalf("reading an unscoped model: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("an unscoped read returned %d rows, want 1", len(got))
	}
}

// TestWriteFillsTheColumnFromTheContext checks that an insert gets the context's
// tenant in the tenant column.
func TestWriteFillsTheColumnFromTheContext(t *testing.T) {
	handle := open(t)

	if err := handle.WithContext(scopedContext("acme")).
		Create(&order{ID: "c", Note: "written"}).Error; err != nil {
		t.Fatalf("writing: %v", err)
	}

	var held string
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw("SELECT tenant_id FROM orders WHERE id = 'c'").Scan(&held).Error; err != nil {
		t.Fatalf("reading the column back: %v", err)
	}
	if held != "acme" {
		t.Fatalf("the column holds %q, want the context's tenant", held)
	}
}

// TestWriteNamingAnotherTenantIsRefused checks that an insert naming another
// tenant fails with ErrExplicitTenantMismatch.
func TestWriteNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)

	err := handle.WithContext(scopedContext("acme")).
		Create(&order{Model: tenant.Model{TenantID: "globex"}, ID: "d"}).Error
	if !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Fatalf("writing another tenant's row gave %v, want ErrExplicitTenantMismatch", err)
	}
}

// TestWriteNamingTheSameTenantIsAllowed checks that an insert naming the
// context's own tenant succeeds.
func TestWriteNamingTheSameTenantIsAllowed(t *testing.T) {
	handle := open(t)

	if err := handle.WithContext(scopedContext("acme")).
		Create(&order{Model: tenant.Model{TenantID: "acme"}, ID: "e"}).Error; err != nil {
		t.Fatalf("writing with a matching column: %v", err)
	}
}

// TestWriteWithoutATenantIsRefused checks that an insert with no tenant in the
// context fails with ErrNoTenantInContext.
func TestWriteWithoutATenantIsRefused(t *testing.T) {
	handle := open(t)

	err := handle.WithContext(context.Background()).Create(&order{ID: "f"}).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("writing with no tenant gave %v, want ErrNoTenantInContext", err)
	}
}

// TestAnOrTheCallerWroteStaysInsideTheTenant checks that an OR in the caller's
// conditions cannot carry a read outside the tenant.
func TestAnOrTheCallerWroteStaysInsideTheTenant(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []order
	err := handle.WithContext(scopedContext("acme")).
		Where("note = ?", "for globex").
		Or("id = ?", "no-such-id").
		Find(&got).Error
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("an OR the caller wrote escaped the tenant: got %d rows (%v)", len(got), got)
	}
}

// TestUpdateNamingAnotherTenantIsRefused checks that an update assigning another
// tenant to the tenant column is refused.
func TestUpdateNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where("id = ?", "a").
		Update("tenant_id", "globex").Error
	if !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Fatalf("moving a row into another tenant gave %v, want ErrExplicitTenantMismatch", err)
	}
	assertTenantOf(t, handle, "a", "acme")
}

// TestUpdatesWithAStructNamingAnotherTenantIsRefused checks the same refusal when
// the assignments come from a struct.
func TestUpdatesWithAStructNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where("id = ?", "a").
		Updates(order{Model: tenant.Model{TenantID: "globex"}}).Error
	if !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Fatalf("moving a row into another tenant gave %v, want ErrExplicitTenantMismatch", err)
	}
	assertTenantOf(t, handle, "a", "acme")
}

// TestUpdateReachesOnlyTheContextTenant checks that an update touches only the
// context tenant's rows.
func TestUpdateReachesOnlyTheContextTenant(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	res := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where("note <> ''").Update("note", "touched")
	if res.Error != nil {
		t.Fatalf("updating: %v", res.Error)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("an update touched %d rows, want just acme's", res.RowsAffected)
	}
	assertTenantOf(t, handle, "b", "globex")
}

// TestDeleteReachesOnlyTheContextTenant checks that a delete removes only the
// context tenant's rows.
func TestDeleteReachesOnlyTheContextTenant(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	res := handle.WithContext(scopedContext("acme")).Where("note <> ''").Delete(&order{})
	if res.Error != nil {
		t.Fatalf("deleting: %v", res.Error)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("a delete reached %d rows, want just acme's", res.RowsAffected)
	}
	var left []order
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Find(&left).Error; err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if len(left) != 1 || left[0].ID != "b" {
		t.Fatalf("after a scoped delete the table holds %v, want just globex's row", left)
	}
}

// TestAnUpsertCannotTakeOverAnotherTenantsRow checks that saving a row whose
// identifier another tenant holds lands beside it, because the tenant column is
// part of the key.
func TestAnUpsertCannotTakeOverAnotherTenantsRow(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).
		Save(&order{
			Model: tenant.Model{TenantID: "acme"},
			ID:    "b",
			Note:  "mine now",
		}).Error
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	var held []string
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw("SELECT tenant_id FROM orders WHERE id = ? ORDER BY tenant_id", "b").
		Scan(&held).Error; err != nil {
		t.Fatalf("reading the rows back: %v", err)
	}
	if len(held) != 2 || held[0] != "acme" || held[1] != "globex" {
		t.Fatalf("the rows identified by b belong to %v, want both tenants to keep one", held)
	}
}

// assertTenantOf asserts that exactly one row carries id: a second row with
// another tenant is what a takeover looks like.
func assertTenantOf(t *testing.T, handle *gorm.DB, id, want string) {
	t.Helper()
	var held []string
	err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw("SELECT tenant_id FROM orders WHERE id = ? ORDER BY tenant_id", id).
		Scan(&held).Error
	if err != nil {
		t.Fatalf("reading the tenant of %s back: %v", id, err)
	}
	if len(held) != 1 || held[0] != want {
		t.Fatalf("the rows identified by %s belong to %v, want just %q", id, held, want)
	}
}
