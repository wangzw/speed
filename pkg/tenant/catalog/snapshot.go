package catalog

import (
	"sort"

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
	ID     string `gorm:"column:id;primaryKey"`
	Alias  string `gorm:"column:alias;not null;uniqueIndex"`
	Status int    `gorm:"column:status;not null"`
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
		t := tenant.Tenant{
			ID:     tenant.ID(r.ID),
			Alias:  r.Alias,
			Status: tenant.Status(r.Status),
		}
		s.byID[t.ID] = t
		s.byAlias[t.Alias] = t
		s.ordered = append(s.ordered, t)
	}
	sort.Slice(s.ordered, func(i, j int) bool { return s.ordered[i].ID < s.ordered[j].ID })
	return s
}

// counts reports how many tenants are in each state, for the startup account.
func (s *snapshot) counts() (active, disabled int) {
	for _, t := range s.ordered {
		if t.Status == tenant.StatusActive {
			active++
			continue
		}
		disabled++
	}
	return active, disabled
}
