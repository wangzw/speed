package catalog

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about which column an assignment reaches. GORM
// resolves a key through the model's schema and writes one it cannot resolve
// into the statement as a column name; the database then resolves that name by
// its own rules — SQLite ignores case, PostgreSQL folds a bare name to lower
// case, both skip the blanks around a name GORM writes as it stands, and either
// honours the quotes a name carries. A key the schema does not know therefore
// still reaches the tenant column whenever the database reads it as that
// column, and the filter has to judge it as the tenant column. A target it
// cannot read as one column at all is refused, because it might be that one.

// write is one statement shape that carries an assignment target the caller
// spells, under the context the case gives it.
type write struct {
	name string
	run  func(tx *gorm.DB, target string, value any) error
}

// keyedWrites are the shapes whose target is a key GORM quotes into the
// statement: an update's map, a single-column update, an explicit SET clause,
// an upsert's assignments, and a map insert, alone and in a slice.
var keyedWrites = []write{
	{"Updates with a map", func(tx *gorm.DB, key string, v any) error {
		return tx.Model(&order{}).Where("id = ?", "a").Updates(map[string]any{key: v}).Error
	}},
	{"Update of one column", func(tx *gorm.DB, key string, v any) error {
		return tx.Model(&order{}).Where("id = ?", "a").Update(key, v).Error
	}},
	{"an explicit SET clause", func(tx *gorm.DB, key string, v any) error {
		return tx.Model(&order{}).Where("id = ?", "a").
			Clauses(clause.Set{{Column: clause.Column{Name: key}, Value: v}}).
			Updates(map[string]any{}).Error
	}},
	{"an upsert's assignments", func(tx *gorm.DB, key string, v any) error {
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
			DoUpdates: clause.Set{{Column: clause.Column{Name: key}, Value: v}},
		}).Create(&order{ID: "a", Note: "again"}).Error
	}},
	{"a map insert", func(tx *gorm.DB, key string, v any) error {
		return tx.Model(&order{}).Create(map[string]any{"id": "a", "note": "inserted", key: v}).Error
	}},
	{"a slice of maps inserted", func(tx *gorm.DB, key string, v any) error {
		return tx.Model(&order{}).Create(&[]map[string]any{{"id": "a", "note": "inserted", key: v}}).Error
	}},
}

// rawWrites are the shapes whose target is a column GORM writes into the
// statement as it stands.
var rawWrites = []write{
	{"an explicit SET clause", func(tx *gorm.DB, name string, v any) error {
		return tx.Model(&order{}).Where("id = ?", "a").
			Clauses(clause.Set{{Column: clause.Column{Name: name, Raw: true}, Value: v}}).
			Updates(map[string]any{}).Error
	}},
	{"an upsert's assignments", func(tx *gorm.DB, name string, v any) error {
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
			DoUpdates: clause.Set{{Column: clause.Column{Name: name, Raw: true}, Value: v}},
		}).Create(&order{ID: "a", Note: "again"}).Error
	}},
}

// requireRowsUnmoved asserts that the table still holds the two seeded rows
// and nothing else: acme's a and globex's b.
func requireRowsUnmoved(t *testing.T, handle *gorm.DB) {
	t.Helper()
	got := tenantsOf(t, handle)
	if len(got) != 2 || len(got["a"]) != 1 || got["a"][0] != "acme" ||
		len(got["b"]) != 1 || got["b"][0] != "globex" {
		t.Fatalf("the refused write left the table as %v, want a in acme and b in globex", got)
	}
}

// requireEveryShapeRefused drives each shape with each target under a tenant
// that is not the value written, and under crossing tenants with the empty
// value, and asserts every one of them refused with the table as seeded.
func requireEveryShapeRefused(t *testing.T, open func(*testing.T) *gorm.DB, writes []write, targets []string) {
	t.Helper()
	for _, w := range writes {
		for _, target := range targets {
			t.Run(w.name+"/"+target, func(t *testing.T) {
				handle := open(t)
				seed(t, handle)

				err := w.run(handle.WithContext(scopedContext("acme")), target, "globex")
				requireMismatch(t, err)
				requireRowsUnmoved(t, handle)

				err = w.run(handle.WithContext(crossContext()), target, "")
				if !errors.Is(err, tenant.ErrNoTenantInContext) && !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
					t.Fatalf("a write across tenants emptying the column as %q gave %v, want a refusal", target, err)
				}
				requireRowsUnmoved(t, handle)
			})
		}
	}
}

// SQLite reads every one of these as the tenant column: it ignores case, and
// a name carrying its own backquotes is written as that quoted name.
func TestAKeySQLiteReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, open, keyedWrites,
		[]string{"tEnAnT_iD", "TeNaNt_Id", "`tenant_id`", "`TENANT_ID`"})
}

// A raw target is read by the database as SQL: around blanks, in any case,
// quoted or not, and one that is no single column name at all may name the
// tenant column among others.
func TestARawTargetSQLiteReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, open, rawWrites,
		[]string{" tenant_id", "TENANT_ID ", `"Tenant_Id"`, "[tenant_id]", "tenant_id = 'globex', note"})
}

// PostgreSQL reads a name carrying its own double quotes as that quoted name.
func TestAKeyPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, keyedWrites, []string{`"tenant_id"`})
}

// PostgreSQL folds a bare raw name to lower case and skips the blanks around
// it.
func TestARawTargetPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, rawWrites,
		[]string{" tenant_id", "TeNaNt_Id", `"tenant_id"`, "tenant_id = 'globex', note"})
}

// grant is a scoped model whose own key is not named id, so GORM has no
// primary field it prefers and renders the primary key placeholder as the
// first column of the model — the tenant column.
type grant struct {
	tenant.Model
	UserID string `gorm:"column:user_id;primaryKey"`
	Role   string `gorm:"column:role"`
}

func (grant) TableName() string { return "grants" }

// GORM writes its primary key placeholder, as a key or as a column, as the
// column it stands for.
func TestThePrimaryKeyPlaceholderIsJudgedAsTheColumnItStandsFor(t *testing.T) {
	for name, run := range map[string]func(tx *gorm.DB) error{
		"a key": func(tx *gorm.DB) error {
			return tx.Model(&grant{}).Where("user_id = ?", "u").
				Updates(map[string]any{clause.PrimaryKey: "globex"}).Error
		},
		"a SET column": func(tx *gorm.DB) error {
			return tx.Model(&grant{}).Where("user_id = ?", "u").
				Clauses(clause.Set{{Column: clause.Column{Name: clause.PrimaryKey}, Value: "globex"}}).
				Updates(map[string]any{}).Error
		},
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
			if err := handle.Exec("CREATE TABLE grants (tenant_id TEXT NOT NULL, user_id TEXT NOT NULL, " +
				"role TEXT, PRIMARY KEY (tenant_id, user_id))").Error; err != nil {
				t.Fatalf("creating the table: %v", err)
			}
			if err := handle.WithContext(scopedContext("acme")).
				Create(&grant{UserID: "u", Role: "owner"}).Error; err != nil {
				t.Fatalf("seeding: %v", err)
			}
			requireMismatch(t, run(handle.WithContext(scopedContext("acme"))))
			var tenants []string
			if err := handle.WithContext(crossContext()).
				Raw("SELECT tenant_id FROM grants").Scan(&tenants).Error; err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if len(tenants) != 1 || tenants[0] != "acme" {
				t.Fatalf("the refused write left the grant in %v, want it in acme", tenants)
			}
		})
	}
}

// GORM renders an update's assignments from its destination whenever the
// destination yields any, in place of a SET clause the caller also wrote; the
// destination is what reaches the table, so it is judged too.
func TestAnUpdateCarryingASetClauseAndAMapJudgesTheMap(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&order{}).Where("id = ?", "a").
		Clauses(clause.Set{{Column: clause.Column{Name: "note"}, Value: "set"}}).
		Updates(map[string]any{"tenant_id": "globex"}).Error
	requireMismatch(t, err)
	requireRowsUnmoved(t, handle)
}

// The judgement does not refuse what reaches another column: a key or a raw
// target that reads as some other column is assigned as written.
func TestATargetNamingAnotherColumnIsAssigned(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	ctx := scopedContext("acme")
	if err := handle.WithContext(ctx).Model(&order{}).Where("id = ?", "a").
		Updates(map[string]any{"NOTE": "upper"}).Error; err != nil {
		t.Fatalf("an update by a key naming another column: %v", err)
	}
	if err := handle.WithContext(ctx).Model(&order{}).Where("id = ?", "a").
		Clauses(clause.Set{{Column: clause.Column{Name: ` "note" `, Raw: true}, Value: "raw"}}).
		Updates(map[string]any{}).Error; err != nil {
		t.Fatalf("an update by a raw target naming another column: %v", err)
	}
	var o order
	if err := handle.WithContext(ctx).First(&o, "id = ?", "a").Error; err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if o.Note != "raw" || o.TenantID != "acme" {
		t.Fatalf("the row reads %+v, want acme's with the note rewritten", o)
	}
	requireRowTenant(t, handle, "a", "acme")
}
