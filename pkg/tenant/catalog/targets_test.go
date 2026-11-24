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

type write struct {
	name string
	run  func(tx *gorm.DB, target string, value any) error
}

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

func requireRowsUnmoved(t *testing.T, handle *gorm.DB) {
	t.Helper()
	got := tenantsOf(t, handle)
	if len(got) != 2 || len(got["a"]) != 1 || got["a"][0] != "acme" ||
		len(got["b"]) != 1 || got["b"][0] != "globex" {
		t.Fatalf("the refused write left the table as %v, want a in acme and b in globex", got)
	}
}

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

func TestAKeySQLiteReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, open, keyedWrites,
		[]string{"tEnAnT_iD", "TeNaNt_Id", "`tenant_id`", "`TENANT_ID`"})
}

func TestARawTargetSQLiteReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, open, rawWrites,
		[]string{" tenant_id", "TENANT_ID ", `"Tenant_Id"`, "[tenant_id]", "tenant_id = 'globex', note"})
}

type grant struct {
	tenant.Model
	UserID string `gorm:"column:user_id;primaryKey"`
	Role   string `gorm:"column:role"`
}

func (grant) TableName() string { return "grants" }

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

func TestAnUpdateCarryingASetClauseAndAMapJudgesTheMap(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&order{}).Where("id = ?", "a").
		Clauses(clause.Set{{Column: clause.Column{Name: "note"}, Value: "set"}}).
		Updates(map[string]any{"tenant_id": "globex"}).Error
	requireMismatch(t, err)
	requireRowsUnmoved(t, handle)
}

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

type noted struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:Note"`
}

func (noted) TableName() string { return "orders" }

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

func TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWrites(t *testing.T) {
	requirePatchesRefused(t, open, []string{"TENANT_ID", "Tenant_Id", "`tenant_id`", "tenant_id"})
}

type shadowed struct {
	Owner string `gorm:"column:TENANT_ID"`
	tenant.Model
	ID string `gorm:"column:id;primaryKey"`
}

func (shadowed) TableName() string { return "orders" }

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

type duplicated struct {
	tenant.Model
	ID   string    `gorm:"column:id;primaryKey"`
	Note string    `gorm:"column:note"`
	Meta orderMeta `gorm:"embedded"`
}

func (duplicated) TableName() string { return "orders" }

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

type defaultedMeta struct {
	TenantID string `gorm:"default:(-)"`
}

type defaultedRepeat struct {
	tenant.Model
	ID   string        `gorm:"column:id;primaryKey"`
	Note string        `gorm:"column:note"`
	Meta defaultedMeta `gorm:"embedded"`
}

func (defaultedRepeat) TableName() string { return "orders" }

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

type stamping struct {
	tenant.Model
	ID      string `gorm:"column:id;primaryKey"`
	Note    string `gorm:"column:note"`
	Touched int64  `gorm:"column:TENANT_ID;autoUpdateTime"`
}

func (stamping) TableName() string { return "orders" }

func TestAnUpdateStampingASpellingOfTheTenantColumnIsJudged(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&stamping{}).Where("id = ?", "a").
		Updates(map[string]any{"note": "touched"}).Error
	requireMismatch(t, err)
	requireRowsUnmoved(t, handle)
}

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
