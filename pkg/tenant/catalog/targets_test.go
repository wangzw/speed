package catalog

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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

// noted is a scoped model whose note column is named like an exported Go
// identifier. GORM assigns a model's column from the field of the updating type
// it finds under that column's name or under that field's Go name, and writes
// the column that field carries: a patch type's Note field reaches whatever
// column its own tag names.
type noted struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:Note"`
}

func (noted) TableName() string { return "orders" }

// patchWriting is a type of its own with one field, Note, carrying this value
// under a column spelled as given.
func patchWriting(column, value string) any {
	typ := reflect.StructOf([]reflect.StructField{{
		Name: "Note",
		Type: reflect.TypeOf(""),
		Tag:  reflect.StructTag("gorm:" + strconv.Quote("column:"+column)),
	}})
	v := reflect.New(typ).Elem()
	v.Field(0).SetString(value)
	return v.Interface()
}

// requirePatchesRefused drives an update whose assignments are carried in
// another type, with the field GORM finds for the model's Note column writing
// each of these columns, under a tenant that is not the value written and
// across tenants with a value no tenant has.
func requirePatchesRefused(t *testing.T, open func(*testing.T) *gorm.DB, columns []string) {
	t.Helper()
	for _, column := range columns {
		t.Run(column, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			patch := func(ctx context.Context, value string) error {
				return handle.WithContext(ctx).Model(&noted{}).Where("id = ?", "a").
					Updates(patchWriting(column, value)).Error
			}
			requireMismatch(t, patch(scopedContext("acme"), "globex"))
			requireRowsUnmoved(t, handle)
			if err := patch(crossContext(), "acme corp"); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("a patch across tenants writing a blank into %q gave %v, want ErrInvalidTenant", column, err)
			}
			requireRowsUnmoved(t, handle)
		})
	}
}

// The column an update from another type writes is the one the field GORM
// found carries, read the way the database reads it — not whichever field of
// that type is named like the tenant column.
func TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWrites(t *testing.T) {
	requirePatchesRefused(t, open, []string{"TENANT_ID", "Tenant_Id", "`tenant_id`", "tenant_id"})
}

func TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWritesOnPostgres(t *testing.T) {
	requirePatchesRefused(t, openFilteredPostgres, []string{`"tenant_id"`, "tenant_id"})
}

// shadowed is a scoped model carrying, ahead of the convention, a field of its
// own whose column the database reads as the tenant column. GORM writes both
// columns; SQLite keeps the first of an insert's and the last of an update's.
type shadowed struct {
	Owner string `gorm:"column:TENANT_ID"`
	tenant.Model
	ID string `gorm:"column:id;primaryKey"`
}

func (shadowed) TableName() string { return "orders" }

// A field of the model writing the tenant column under another spelling is
// judged as the tenant column is, on an insert and on an update alike, and
// whatever a selection names it by: GORM selects a map key under the column the
// schema resolves it to, so a selection naming the field by its Go name or by
// its column's spelling writes it.
func TestAFieldOfTheModelWritingTheTenantColumnIsJudged(t *testing.T) {
	ctx := scopedContext("acme")
	for name, write := range map[string]func(tx *gorm.DB) error{
		"a struct insert": func(tx *gorm.DB) error { return tx.Create(&shadowed{Owner: "globex", ID: "x"}).Error },
		"a map insert keyed by the field": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Create(map[string]any{"ID": "x", "Owner": "globex"}).Error
		},
		"a map insert selecting the field": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Select("Owner", "ID").
				Create(map[string]any{"ID": "x", "Owner": "globex"}).Error
		},
		"a struct update": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Where("id = ?", "a").Updates(shadowed{Owner: "globex"}).Error
		},
		"a map update keyed by the field": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Where("id = ?", "a").Updates(map[string]any{"Owner": "globex"}).Error
		},
		"a map update selecting the field": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Where("id = ?", "a").Select("Owner").
				Updates(map[string]any{"Owner": "globex"}).Error
		},
		"a map update selecting the field's column": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Where("id = ?", "a").Select("TENANT_ID").
				Updates(map[string]any{"Owner": "globex"}).Error
		},
		"an update of one column selecting the field": func(tx *gorm.DB) error {
			return tx.Model(&shadowed{}).Where("id = ?", "a").Select("Owner").Update("Owner", "globex").Error
		},
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			if err := write(handle.WithContext(ctx)); !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
				t.Fatalf("writing another tenant under a second spelling gave %v, want ErrExplicitTenantMismatch",
					err)
			}
			requireRowsUnmoved(t, handle)
		})
	}

	t.Run("an insert naming the context's tenant", func(t *testing.T) {
		handle := open(t)
		seed(t, handle)
		if err := handle.WithContext(ctx).Create(&shadowed{Owner: "acme", ID: "y"}).Error; err != nil {
			t.Fatalf("an insert whose second spelling names the context's tenant: %v", err)
		}
		requireRowTenant(t, handle, "y", "acme")
	})
}

// duplicated is a scoped model with a struct embedded under a name that
// repeats the convention's column exactly. GORM binds the column to the
// convention's field, declared first, and never writes the embedded one.
type duplicated struct {
	tenant.Model
	ID   string    `gorm:"column:id;primaryKey"`
	Note string    `gorm:"column:note"`
	Meta orderMeta `gorm:"embedded"`
}

func (duplicated) TableName() string { return "orders" }

// A field GORM never writes is not a spelling of the tenant column: an insert
// binds each column from the one field GORM binds it to, so a field repeating
// the column exactly — whatever it holds — does not reach the table, and
// judging it would refuse an insert that writes the context's tenant.
func TestAFieldGORMNeverWritesIsNotJudged(t *testing.T) {
	for name, row := range map[string]*duplicated{
		"left unwritten":        {ID: "x"},
		"naming another tenant": {ID: "x", Meta: orderMeta{TenantID: "globex"}},
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			if err := handle.WithContext(scopedContext("acme")).Create(row).Error; err != nil {
				t.Fatalf("an insert whose repeated column GORM does not write gave %v, want it written", err)
			}
			requireRowTenant(t, handle, "x", "acme")
		})
	}

	t.Run("across tenants", func(t *testing.T) {
		handle := open(t)
		seed(t, handle)
		row := &duplicated{Model: tenant.Model{TenantID: "globex"}, ID: "x"}
		if err := handle.WithContext(crossContext()).Create(row).Error; err != nil {
			t.Fatalf("an insert across tenants naming its tenant gave %v, want it written", err)
		}
		requireRowTenant(t, handle, "x", "globex")
	})
}

// defaultedMeta repeats the convention's column exactly, with a default the
// database supplies.
type defaultedMeta struct {
	TenantID string `gorm:"default:(-)"`
}

// defaultedRepeat is a scoped model embedding it under a name. GORM binds the
// column to the convention's field, and writes the embedded one as well
// whenever it holds a value, because its default is the database's.
type defaultedRepeat struct {
	tenant.Model
	ID   string        `gorm:"column:id;primaryKey"`
	Note string        `gorm:"column:note"`
	Meta defaultedMeta `gorm:"embedded"`
}

func (defaultedRepeat) TableName() string { return "orders" }

// A field repeating the tenant column exactly is judged when GORM writes it: an
// insert carries a field whose default is the database's, and that holds a
// value, as a column of its own beside the one the column is bound to.
func TestARepeatedFieldGORMWritesIsJudged(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	acme := handle.WithContext(scopedContext("acme"))
	requireMismatch(t, acme.Create(&defaultedRepeat{ID: "x", Meta: defaultedMeta{TenantID: "globex"}}).Error)
	if rows := tenantsOf(t, handle)["x"]; len(rows) != 0 {
		t.Fatalf("a refused insert left rows %v", rows)
	}
	if err := acme.Create(&defaultedRepeat{ID: "y"}).Error; err != nil {
		t.Fatalf("an insert whose repeat holds nothing, and is not written, gave %v", err)
	}
	requireRowTenant(t, handle, "y", "acme")
}

// stamping is a scoped model carrying a field that GORM stamps with the update
// time, under another spelling of the tenant column.
type stamping struct {
	tenant.Model
	ID      string `gorm:"column:id;primaryKey"`
	Note    string `gorm:"column:note"`
	Touched int64  `gorm:"column:TENANT_ID;autoUpdateTime"`
}

func (stamping) TableName() string { return "orders" }

// GORM stamps the update time into a field the map of an update leaves out,
// and a field doing that under a spelling of the tenant column writes the time
// into it, which is not the context's tenant.
func TestAnUpdateStampingASpellingOfTheTenantColumnIsJudged(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&stamping{}).Where("id = ?", "a").
		Updates(map[string]any{"note": "touched"}).Error
	requireMismatch(t, err)
	requireRowsUnmoved(t, handle)
}

// SQLite keeps the last of an update's duplicate columns, so a write carrying
// the context's tenant under one spelling of the tenant column and another
// tenant under a later one moves the row to the later: every assignment that
// reaches the tenant column is judged, not only the first.
func TestEveryAssignmentReachingTheTenantColumnIsJudged(t *testing.T) {
	type twoSpellings struct {
		TenantID string `gorm:"column:tenant_id"`
		Note     string `gorm:"column:TENANT_ID"`
	}
	for name, write := range map[string]func(tx *gorm.DB) error{
		"a struct from another type": func(tx *gorm.DB) error {
			return tx.Model(&noted{}).Where("id = ?", "a").
				Updates(twoSpellings{TenantID: "acme", Note: "globex"}).Error
		},
		"an explicit SET clause": func(tx *gorm.DB) error {
			return tx.Model(&order{}).Where("id = ?", "a").Clauses(clause.Set{
				{Column: clause.Column{Name: "tenant_id"}, Value: "acme"},
				{Column: clause.Column{Name: "TENANT_ID"}, Value: "globex"},
			}).Updates(map[string]any{}).Error
		},
		"an upsert's assignments": func(tx *gorm.DB) error {
			return tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
				DoUpdates: clause.Set{
					{Column: clause.Column{Name: "tenant_id"}, Value: "acme"},
					{Column: clause.Column{Name: "TENANT_ID"}, Value: "globex"},
				},
			}).Create(&order{ID: "a", Note: "again"}).Error
		},
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			requireMismatch(t, write(handle.WithContext(scopedContext("acme"))))
			requireRowsUnmoved(t, handle)
		})
	}
}
