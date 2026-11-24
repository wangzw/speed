package catalog

import (
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

type trail struct {
	tenant.Model
	ID     int64  `gorm:"column:id"`
	Action string `gorm:"column:action"`
}

func (trail) TableName() string { return "trails" }

type nullableTrail struct {
	tenant.Model
	TenantID *string `gorm:"column:tenant_id"`
	ID       int64   `gorm:"column:id"`
	Action   string  `gorm:"column:action"`
}

func (nullableTrail) TableName() string { return "trails" }

// TestAReadOnlyModelReadsANullTenantColumnAsNoTenantsRow checks that a row with
// a null tenant column is not reached by a tenant's read and reads as the empty
// tenant across tenants, without error.
func TestAReadOnlyModelReadsANullTenantColumnAsNoTenantsRow(t *testing.T) {
	for _, d := range filteredDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			if err := handle.Exec(
				"CREATE TABLE trails (id BIGINT PRIMARY KEY, tenant_id TEXT NULL, action TEXT)").Error; err != nil {
				t.Fatalf("creating the table: %v", err)
			}
			if err := handle.WithContext(crossContext()).Exec(
				"INSERT INTO trails (id, tenant_id, action) VALUES (1, 't_acme', 'a'), (2, NULL, 'b')").
				Error; err != nil {
				t.Fatalf("seeding: %v", err)
			}

			var scoped []trail
			if err := handle.WithContext(scopedContext("t_acme")).Find(&scoped).Error; err != nil {
				t.Fatalf("reading under a tenant: %v", err)
			}
			if len(scoped) != 1 || scoped[0].ID != 1 {
				t.Fatalf("a read under t_acme returned %+v, want its one row", scoped)
			}

			var across []trail
			if err := handle.WithContext(crossContext()).Order("id").Find(&across).Error; err != nil {
				t.Fatalf("reading across tenants gave %v, want the rows", err)
			}
			if len(across) != 2 || across[1].ID != 2 || across[1].TenantID != "" {
				t.Fatalf("a read across tenants returned %+v, want both rows, the second with no tenant", across)
			}

			var nullable []struct {
				TenantID *string
				ID       int64
			}
			if err := handle.WithContext(crossContext()).Table("trails").Order("id").
				Find(&nullable).Error; err != nil {
				t.Fatalf("reading across tenants into a nullable field: %v", err)
			}
			if len(nullable) != 2 || nullable[1].TenantID != nil {
				t.Fatalf("a nullable field read %+v, want the second row's tenant null", nullable)
			}

			var scopedNullable []nullableTrail
			if err := handle.WithContext(scopedContext("t_acme")).Find(&scopedNullable).Error; err != nil {
				t.Fatalf("reading under a tenant through a nullable field: %v", err)
			}
			if len(scopedNullable) != 1 || scopedNullable[0].TenantID == nil ||
				*scopedNullable[0].TenantID != "t_acme" {
				t.Fatalf("a read under t_acme through a nullable field returned %+v, want its one row", scopedNullable)
			}
			var acrossNullable []nullableTrail
			if err := handle.WithContext(crossContext()).Order("id").Find(&acrossNullable).Error; err != nil {
				t.Fatalf("reading across tenants through a nullable field: %v", err)
			}
			if len(acrossNullable) != 2 || acrossNullable[1].TenantID != nil {
				t.Fatalf("a read across tenants through a nullable field returned %+v, "+
					"want both rows, the second with a null tenant", acrossNullable)
			}
		})
	}
}
