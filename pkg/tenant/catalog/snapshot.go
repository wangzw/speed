package catalog

import (
	"sort"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// tableName is the roster's table. It is fixed here rather than derived from
// the type, so that renaming the Go type cannot silently point the reads at a
// table the migration did not create.
const tableName = "tenants"

// row is the roster table's shape.
//
// It deliberately does not embed tenant.Model. The filter recognises scoped
// models by that convention, and the roster is the fact scoping is decided
// from: a read of it that needed a tenant first would have nothing to read it
// with. Nothing is special-cased for it — it takes the same path through the
// filter as any other global table, which is the path a model that embeds
// nothing takes.
//
// The alias's `uniqueIndex` tag does not hold that column unique: this type is
// never migrated — the table comes from the module's declared migration — so
// the constraint in that file is what enforces the alias, and the tag records
// the shape for a reader.
type row struct {
	ID         string `gorm:"column:id;primaryKey"`
	Alias      string `gorm:"column:alias;not null;uniqueIndex"`
	Status     int    `gorm:"column:status;not null"`
	DisabledBy int    `gorm:"column:disabled_by;not null"`

	DisplayName  string     `gorm:"column:display_name;not null"`
	Description  string     `gorm:"column:description;not null"`
	ContactEmail string     `gorm:"column:contact_email;not null"`
	Website      string     `gorm:"column:website;not null"`
	CreatedAt    *time.Time `gorm:"column:created_at"`
}

// TableName fixes the table this type maps to.
func (row) TableName() string { return tableName }

// snapshot is the roster as of one read. It is replaced whole rather than
// amended, so a reader holding one sees a consistent roster for as long as it
// holds it — which is what makes two resolutions inside one request agree.
type snapshot struct {
	byID    map[tenant.ID]tenant.Tenant
	byAlias map[string]tenant.Tenant
	ordered []tenant.Tenant
}

// buildSnapshot indexes the rows two ways and keeps the order.
//
// The alias index is built by assignment rather than merged, which is why
// uniqueness cannot be left to this step: two rows sharing an alias would
// overwrite one another here and the roster would quietly lose one of them.
// The column's unique index and the check in Put are what keep that from
// happening; this is the reason both are load-bearing.
func buildSnapshot(rows []row) *snapshot {
	s := &snapshot{
		byID:    make(map[tenant.ID]tenant.Tenant, len(rows)),
		byAlias: make(map[string]tenant.Tenant, len(rows)),
		ordered: make([]tenant.Tenant, 0, len(rows)),
	}
	for _, r := range rows {
		t := tenantOf(r)
		s.byID[t.ID] = t
		s.byAlias[t.Alias] = t
		s.ordered = append(s.ordered, t)
	}
	sort.Slice(s.ordered, func(i, j int) bool { return s.ordered[i].ID < s.ordered[j].ID })
	return s
}

// tenantOf reads one row as the roster serves it: the status and the disable
// source through their closed sets, and the profile and the creation time as
// the table holds them. A creation time the table does not hold stays nil.
func tenantOf(r row) tenant.Tenant {
	return tenant.Tenant{
		ID:         tenant.ID(r.ID),
		Alias:      r.Alias,
		Status:     tenant.Status(r.Status),
		DisabledBy: disabledByOf(r.DisabledBy),
		Profile: tenant.Profile{
			DisplayName:  r.DisplayName,
			Description:  r.Description,
			ContactEmail: r.ContactEmail,
			Website:      r.Website,
		},
		CreatedAt: r.CreatedAt,
	}
}

// disabledByOf reads the source column as the roster serves it: only the
// account's value reads as the account's, and everything else — the operator's
// value, a value this build does not know, and the default an existing row
// took — reads as the operator's. An unexplained disable must never be
// revocable by account state.
func disabledByOf(source int) tenant.DisabledBy {
	if source == int(tenant.DisabledByAccount) {
		return tenant.DisabledByAccount
	}
	return tenant.DisabledByOperator
}

// counts reports how many tenants are in each state, for the startup account.
// The three counts are disjoint and their sum is the roster's size; a status
// this build does not know reads as disabled, as it does everywhere else.
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
