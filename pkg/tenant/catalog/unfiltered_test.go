package catalog

import (
	"context"
	"errors"
	"testing"

	sqlitedriver "github.com/glebarez/sqlite"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/tenant"
)

// ordersDDL is the scoped table these cases write, keyed the way the
// convention requires.
const ordersDDL = "CREATE TABLE orders (tenant_id TEXT NOT NULL, id TEXT NOT NULL, " +
	"note TEXT, PRIMARY KEY (tenant_id, id))"

// openUnfiltered is a handle with the orders table and no filter: what a
// command that runs a scoped model without the roster module gets.
func openUnfiltered(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := handle.Exec(ordersDDL).Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	return handle
}

// openUnfilteredPostgres is the same handle on the PostgreSQL fixture. The
// driver there binds arguments itself rather than through database/sql's
// converter, so the refusal has a second path to the statement to be proven on.
func openUnfilteredPostgres(t *testing.T) *gorm.DB {
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
	if err := handle.Exec(ordersDDL).Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	return handle
}

// requireUnfilteredInsertRefused is the case itself, on either handle. With no
// filter installed nothing fills the column, and the NOT NULL a migration
// declares does not stop an empty string: the identifier refusing to be bound
// empty is what keeps the row that no tenant owns out of the table.
func requireUnfilteredInsertRefused(t *testing.T, handle *gorm.DB) {
	t.Helper()
	err := handle.WithContext(context.Background()).Create(&order{ID: "x"}).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("an unfiltered insert with no tenant gave %v, want ErrNoTenantInContext", err)
	}
	var n int64
	if err := handle.Raw("SELECT count(*) FROM orders").Scan(&n).Error; err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 0 {
		t.Fatalf("a refused insert left %d rows", n)
	}

	if err := handle.Create(&order{Model: tenant.Model{TenantID: "acme"}, ID: "y"}).Error; err != nil {
		t.Fatalf("an unfiltered insert naming its tenant: %v", err)
	}
}

func TestAnInsertWithNoFilterAndNoTenantFails(t *testing.T) {
	requireUnfilteredInsertRefused(t, openUnfiltered(t))
}

func TestAnInsertWithNoFilterAndNoTenantFailsOnPostgres(t *testing.T) {
	requireUnfilteredInsertRefused(t, openUnfilteredPostgres(t))
}

// A delete addressed by a key whose tenant half is empty names no tenant's
// row. Unfiltered it used to reach nothing and report success; it now fails
// where the empty identifier is bound.
func TestAnUnfilteredDeleteByAKeyWithNoTenantFails(t *testing.T) {
	handle := openUnfiltered(t)
	err := handle.Delete(&order{ID: "x"}).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("an unfiltered delete by a key with no tenant gave %v, want ErrNoTenantInContext", err)
	}
}
