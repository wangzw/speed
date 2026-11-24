package catalog

import (
	"sort"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

const tableName = "tenants"

type row struct {
	ID         string `gorm:"column:id;primaryKey"`
	Name       string `gorm:"column:name;not null;uniqueIndex"`
	Status     int    `gorm:"column:status;not null"`
	DisabledBy int    `gorm:"column:disabled_by;not null"`

	OwnerAccount string `gorm:"column:owner_account;not null"`

	DisplayName  string     `gorm:"column:display_name;not null"`
	Description  string     `gorm:"column:description;not null"`
	ContactEmail string     `gorm:"column:contact_email;not null"`
	Website      string     `gorm:"column:website;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (row) TableName() string { return tableName }

type snapshot struct {
	byID    map[tenant.ID]tenant.Tenant
	byName  map[string]tenant.Tenant
	ordered []tenant.Tenant
}

func buildSnapshot(rows []row) *snapshot {
	s := &snapshot{
		byID:    make(map[tenant.ID]tenant.Tenant, len(rows)),
		byName:  make(map[string]tenant.Tenant, len(rows)),
		ordered: make([]tenant.Tenant, 0, len(rows)),
	}
	for _, r := range rows {
		t := tenantOf(r)
		s.byID[t.ID] = t
		s.byName[t.Name] = t
		s.ordered = append(s.ordered, t)
	}
	sort.Slice(s.ordered, func(i, j int) bool { return s.ordered[i].Name < s.ordered[j].Name })
	return s
}

func tenantOf(r row) tenant.Tenant {
	return tenant.Tenant{
		ID:           tenant.ID(r.ID),
		Name:         r.Name,
		Status:       tenant.Status(r.Status),
		DisabledBy:   disabledByOf(r.DisabledBy),
		OwnerAccount: r.OwnerAccount,
		Profile: tenant.Profile{
			DisplayName:  r.DisplayName,
			Description:  r.Description,
			ContactEmail: r.ContactEmail,
			Website:      r.Website,
		},
		CreatedAt: r.CreatedAt,
		DeletedAt: r.DeletedAt,
	}
}

func disabledByOf(source int) tenant.DisabledBy {
	if source == int(tenant.DisabledByAccount) {
		return tenant.DisabledByAccount
	}
	return tenant.DisabledByOperator
}

func (s *snapshot) counts() (active, disabled, deleted int) {
	for _, t := range s.ordered {
		switch t.Status {
		case tenant.StatusActive:
			active++
		case tenant.StatusDeleted:
			deleted++
		default:
			disabled++
		}
	}
	return active, disabled, deleted
}
