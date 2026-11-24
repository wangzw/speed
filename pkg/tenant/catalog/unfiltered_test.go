package catalog

import (
	"context"
	"errors"
	"testing"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/tenant"
)

const ordersDDL = "CREATE TABLE orders (tenant_id TEXT NOT NULL, id TEXT NOT NULL, " +
	"note TEXT, PRIMARY KEY (tenant_id, id))"

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

func TestAnUnfilteredDeleteByAKeyWithNoTenantFails(t *testing.T) {
	handle := openUnfiltered(t)
	err := handle.Delete(&order{ID: "x"}).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("an unfiltered delete by a key with no tenant gave %v, want ErrNoTenantInContext", err)
	}
}
