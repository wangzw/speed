package catalog

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/tenant"
)

func requireNoRow(t *testing.T, handle *gorm.DB, id string) {
	t.Helper()
	if got := tenantsOf(t, handle)[id]; len(got) != 0 {
		t.Fatalf("a refused write left rows identified by %s, belonging to %q", id, got)
	}
}

func noPanic(t *testing.T, run func() error) (err error) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("the statement panicked: %v", p)
		}
	}()
	return run()
}

// TestUpdateNamingAnotherTenantUnderAnyKeyIsRefused checks that an update
// naming another tenant is refused under every key GORM resolves to the tenant
// column.
func TestUpdateNamingAnotherTenantUnderAnyKeyIsRefused(t *testing.T) {
	for _, key := range []string{"tenant_id", "TenantID", "TenantId", "tenantId", "tenantID", "TENANT_ID", "Tenant_ID"} {
		t.Run(key, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			err := handle.WithContext(scopedContext("acme")).Model(&order{}).
				Where("id = ?", "a").Updates(map[string]any{key: "globex"}).Error
			requireMismatch(t, err)
			requireRowTenant(t, handle, "a", "acme")
		})
	}
}

// TestUpdateWritingAnythingButTheContextTenantIsRefused checks that an update
// writing an expression or the empty value to the tenant column is refused.
func TestUpdateWritingAnythingButTheContextTenantIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"an expression", gorm.Expr("?", "globex")},
		{"a raw expression", clause.Expr{SQL: "'globex'"}},
		{"the empty value", ""},
		{"a null", nil},
		{"another tenant's identifier", tenant.ID("globex")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			err := handle.WithContext(scopedContext("acme")).Model(&order{}).
				Where("id = ?", "a").Update("tenant_id", tc.value).Error
			requireMismatch(t, err)
			requireRowTenant(t, handle, "a", "acme")
		})
	}
}

// TestUpdateWritingTheContextTenantIsAllowed checks that an update writing the
// context's own identifier is allowed.
func TestUpdateWritingTheContextTenantIsAllowed(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	ctx := scopedContext("acme")
	if err := handle.WithContext(ctx).Model(&order{}).Where("id = ?", "a").
		Update("tenant_id", "acme").Error; err != nil {
		t.Fatalf("updating the column to the context's tenant: %v", err)
	}
	if err := handle.WithContext(ctx).Model(&order{}).Where("id = ?", "a").
		Updates(map[string]any{"TenantID": tenant.ID("acme"), "note": "same"}).Error; err != nil {
		t.Fatalf("updating the column to the context's tenant by field name: %v", err)
	}
	requireRowTenant(t, handle, "a", "acme")
}

// TestUpdateSelectingAnUnwrittenTenantColumnIsRefused checks that a struct
// update selecting the tenant column is refused, since it writes the column
// even at its zero value.
func TestUpdateSelectingAnUnwrittenTenantColumnIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where("id = ?", "a").Select("*").Updates(order{Note: "touched"}).Error
	requireMismatch(t, err)
	requireRowTenant(t, handle, "a", "acme")
}

// TestSavingARowReadBackIsAllowed checks that saving a row read back is not a
// write naming another tenant.
func TestSavingARowReadBackIsAllowed(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	ctx := scopedContext("acme")

	var o order
	if err := handle.WithContext(ctx).First(&o, "id = ?", "a").Error; err != nil {
		t.Fatalf("reading: %v", err)
	}
	o.Note = "saved"
	if err := handle.WithContext(ctx).Save(&o).Error; err != nil {
		t.Fatalf("saving: %v", err)
	}
	requireRowTenant(t, handle, "a", "acme")
}

// TestSavingAStructWithoutItsTenantHalfFailsRatherThanRewrites checks that
// saving a struct with an unwritten tenant half, which GORM decides is an
// insert, fails on the key and leaves the row untouched.
func TestSavingAStructWithoutItsTenantHalfFailsRatherThanRewrites(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Save(&order{ID: "a", Note: "saved"}).Error
	if err == nil {
		t.Fatal("a save by a key without its tenant half reported success")
	}
	var o order
	if err := handle.WithContext(scopedContext("acme")).First(&o, "id = ?", "a").Error; err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if o.Note != "for acme" {
		t.Fatalf("the failed save left the row as %+v, want it untouched", o)
	}
}

// TestSavingAStructWithTheContextTenantWrittenRewritesTheRow checks that a save
// writing the context's tenant in the key's tenant half updates the row.
func TestSavingAStructWithTheContextTenantWrittenRewritesTheRow(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	ctx := scopedContext("acme")

	id, _ := tenant.FromContext(ctx)
	if err := handle.WithContext(ctx).Save(&order{Model: tenant.Model{TenantID: id}, ID: "a", Note: "saved"}).
		Error; err != nil {
		t.Fatalf("saving: %v", err)
	}
	var o order
	if err := handle.WithContext(ctx).First(&o, "id = ?", "a").Error; err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if o.Note != "saved" {
		t.Fatalf("the save left the row as %+v, want it rewritten", o)
	}
	requireRowsUnmoved(t, handle)
}

// TestAnUpsertByKeyRewritesTheContextTenantsOwnRow checks that an upsert with
// the tenant half left to the filter conflicts on the context tenant's own row,
// never another tenant's sharing the rest of the key.
func TestAnUpsertByKeyRewritesTheContextTenantsOwnRow(t *testing.T) {
	requireUpsertByKey(t, open(t))
}

func requireUpsertByKey(t *testing.T, handle *gorm.DB) {
	t.Helper()
	seed(t, handle)
	ctx := scopedContext("acme")
	upsert := func(o *order) error {
		return handle.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(o).Error
	}

	if err := upsert(&order{ID: "a", Note: "rewritten"}); err != nil {
		t.Fatalf("upserting a row the tenant has: %v", err)
	}
	if err := upsert(&order{ID: "b", Note: "acme's own b"}); err != nil {
		t.Fatalf("upserting a key another tenant uses: %v", err)
	}

	var rows []struct{ TenantID, ID, Note string }
	if err := handle.WithContext(crossContext()).
		Raw("SELECT tenant_id, id, note FROM orders ORDER BY id, tenant_id").Scan(&rows).Error; err != nil {
		t.Fatalf("reading back: %v", err)
	}
	want := []struct{ TenantID, ID, Note string }{
		{"acme", "a", "rewritten"}, {"acme", "b", "acme's own b"}, {"globex", "b", "for globex"},
	}
	if len(rows) != len(want) {
		t.Fatalf("the table holds %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("the table holds %+v, want %+v", rows, want)
		}
	}
}

// TestUpdateThroughAnExplicitSetClauseIsChecked checks that an explicit SET
// clause, which GORM renders as it stands, is judged.
func TestUpdateThroughAnExplicitSetClauseIsChecked(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where("id = ?", "a").
		Clauses(clause.Set{{Column: clause.Column{Name: "tenant_id"}, Value: "globex"}}).
		Updates(map[string]any{}).Error
	requireMismatch(t, err)
	requireRowTenant(t, handle, "a", "acme")
}

type orderPatch struct {
	Note string `gorm:"column:note"`
}

type orderPatchWithTenant struct {
	TenantID string `gorm:"column:tenant_id"`
	Note     string `gorm:"column:note"`
}

// TestUpdatesWithAnotherStructTypeIsScoped checks that an update carrying its
// assignments in another struct type is scoped to the context's tenant.
func TestUpdatesWithAnotherStructTypeIsScoped(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var res *gorm.DB
	err := noPanic(t, func() error {
		res = handle.WithContext(scopedContext("acme")).Model(&order{}).
			Where("note <> ''").Updates(orderPatch{Note: "patched"})
		return res.Error
	})
	if err != nil {
		t.Fatalf("patching: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("a patch reached %d rows, want acme's one", res.RowsAffected)
	}
}

// TestUpdatesWithAnotherStructTypeNamingAnotherTenantIsRefused checks that an
// update from another struct type naming another tenant is refused.
func TestUpdatesWithAnotherStructTypeNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := noPanic(t, func() error {
		return handle.WithContext(scopedContext("acme")).Model(&order{}).
			Where("id = ?", "a").Updates(orderPatchWithTenant{TenantID: "globex"}).Error
	})
	requireMismatch(t, err)
	requireRowTenant(t, handle, "a", "acme")
}

// TestCreateByValueFillsTheColumn checks that an insert handed by value, as a
// slice of values or of pointers, lands in the context's tenant.
func TestCreateByValueFillsTheColumn(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
		dest func() any
	}{
		{"a struct value", []string{"v1"}, func() any { return order{ID: "v1"} }},
		{"a slice of values", []string{"v2", "v3"}, func() any { return []order{{ID: "v2"}, {ID: "v3"}} }},
		{"a slice of pointers", []string{"v4", "v5"}, func() any { return []*order{{ID: "v4"}, {ID: "v5"}} }},
		{"a pointer to a slice of pointers", []string{"v6"}, func() any { return &[]*order{{ID: "v6"}} }},
		{"an array value", []string{"v7"}, func() any { return [1]order{{ID: "v7"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := open(t)
			if err := handle.WithContext(scopedContext("acme")).Create(tc.dest()).Error; err != nil {
				t.Fatalf("writing: %v", err)
			}
			for _, id := range tc.ids {
				requireRowTenant(t, handle, id, "acme")
			}
		})
	}
}

// TestCreateByValueNamingAnotherTenantIsRefused checks that an insert by value
// naming another tenant is refused.
func TestCreateByValueNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)
	err := handle.WithContext(scopedContext("acme")).
		Create([]*order{{ID: "w0"}, {Model: tenant.Model{TenantID: "globex"}, ID: "w1"}}).Error
	requireMismatch(t, err)
	requireNoRow(t, handle, "w0")
	requireNoRow(t, handle, "w1")
}

// TestCreateFromAMapIsFilledAndChecked checks that a map insert is filled and
// checked, its keys resolved as an update's are.
func TestCreateFromAMapIsFilledAndChecked(t *testing.T) {
	handle := open(t)
	ctx := scopedContext("acme")

	if err := handle.WithContext(ctx).Model(&order{}).
		Create(map[string]any{"id": "m1", "note": "from a map"}).Error; err != nil {
		t.Fatalf("writing a map: %v", err)
	}
	requireRowTenant(t, handle, "m1", "acme")

	for _, key := range []string{"tenant_id", "TenantID", "tenantId"} {
		err := handle.WithContext(ctx).Model(&order{}).
			Create(map[string]any{"id": "m2", key: "globex"}).Error
		requireMismatch(t, err)
	}
	requireNoRow(t, handle, "m2")

	if err := handle.WithContext(ctx).Model(&order{}).
		Create(map[string]any{"id": "m3", "tenant_id": "acme"}).Error; err != nil {
		t.Fatalf("writing a map naming the context's own tenant: %v", err)
	}
	requireRowTenant(t, handle, "m3", "acme")
}

// TestCreateFromASliceOfMapsIsFilledAndChecked checks that an insert from a
// slice of maps is filled and checked row by row.
func TestCreateFromASliceOfMapsIsFilledAndChecked(t *testing.T) {
	handle := open(t)
	ctx := scopedContext("acme")

	err := noPanic(t, func() error {
		return handle.WithContext(ctx).Model(&order{}).
			Create(&[]map[string]any{{"id": "s1"}, {"id": "s2"}}).Error
	})
	if err != nil {
		t.Fatalf("writing a slice of maps: %v", err)
	}
	requireRowTenant(t, handle, "s1", "acme")
	requireRowTenant(t, handle, "s2", "acme")

	err = noPanic(t, func() error {
		return handle.WithContext(ctx).Model(&order{}).
			Create([]map[string]any{{"id": "s3"}, {"id": "s4", "tenant_id": "globex"}}).Error
	})
	requireMismatch(t, err)
	requireNoRow(t, handle, "s3")
	requireNoRow(t, handle, "s4")
}

// TestAnUpsertAssigningTheTenantColumnIsRefused checks that an upsert assigning
// the tenant column is refused, since it would move the context's own
// conflicting row.
func TestAnUpsertAssigningTheTenantColumnIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{"tenant_id": "globex"}),
	}).Create(&order{ID: "a", Note: "again"}).Error
	requireMismatch(t, err)
	requireRowTenant(t, handle, "a", "acme")
}

// TestAnUpsertAssigningTheColumnFromTheInsertedRowIsAllowed checks that an
// upsert reassigning the column from the row being inserted is allowed.
func TestAnUpsertAssigningTheColumnFromTheInsertedRowIsAllowed(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"tenant_id", "note"}),
	}).Create(&order{ID: "a", Note: "again"}).Error
	if err != nil {
		t.Fatalf("upserting: %v", err)
	}
	requireRowTenant(t, handle, "a", "acme")
}

// TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefused
// checks that an upsert assigning the tenant column from another column or an
// expression over the inserted row is refused.
func TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefused(t *testing.T) {
	requireInsertedRowCopiesRefused(t, open)
}

func requireInsertedRowCopiesRefused(t *testing.T, open func(*testing.T) *gorm.DB) {
	t.Helper()
	for name, from := range map[string]clause.Column{
		"another column":                       {Table: "excluded", Name: "note"},
		"an expression over the tenant column": {Table: "excluded", Name: "tenant_id || 'x'", Raw: true},
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			err := handle.WithContext(scopedContext("acme")).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
				DoUpdates: clause.Set{{Column: clause.Column{Name: "tenant_id"}, Value: from}},
			}).Create(&order{ID: "a", Note: "globex"}).Error
			requireMismatch(t, err)
			requireRowsUnmoved(t, handle)
		})
	}
}

// TestACrossTenantInsertWithAnEmptyTenantColumnIsRefused checks that crossing
// tenants does not allow a row with an empty tenant column, which no scoped
// statement could reach again.
func TestACrossTenantInsertWithAnEmptyTenantColumnIsRefused(t *testing.T) {
	handle := open(t)
	for name, write := range map[string]func(*gorm.DB) error{
		"a struct":      func(tx *gorm.DB) error { return tx.Create(&order{ID: "x"}).Error },
		"a map without": func(tx *gorm.DB) error { return tx.Model(&order{}).Create(map[string]any{"id": "x"}).Error },
		"a map emptying": func(tx *gorm.DB) error {
			return tx.Model(&order{}).Create(map[string]any{"id": "x", "tenant_id": ""}).Error
		},
	} {
		err := write(handle.WithContext(crossContext()))
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Fatalf("a cross-tenant insert from %s with no tenant gave %v, want ErrNoTenantInContext", name, err)
		}
	}
	requireNoRow(t, handle, "x")

	if err := handle.WithContext(crossContext()).
		Create(&order{Model: tenant.Model{TenantID: "globex"}, ID: "y"}).Error; err != nil {
		t.Fatalf("a cross-tenant insert naming its tenant: %v", err)
	}
	requireRowTenant(t, handle, "y", "globex")
}

// TestACrossTenantUpdateEmptyingTheTenantColumnIsRefused checks that a
// cross-tenant update writing an empty tenant column is refused.
func TestACrossTenantUpdateEmptyingTheTenantColumnIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	for name, write := range map[string]func(*gorm.DB) error{
		"the empty value": func(tx *gorm.DB) error { return tx.Update("tenant_id", "").Error },
		"a null":          func(tx *gorm.DB) error { return tx.Update("tenant_id", nil).Error },
		"a blank":         func(tx *gorm.DB) error { return tx.Update("tenant_id", "acme corp").Error },
	} {
		err := write(handle.WithContext(crossContext()).Model(&order{}).Where("id = ?", "a"))
		if !errors.Is(err, tenant.ErrNoTenantInContext) && !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("a cross-tenant update writing %s gave %v, want a refusal", name, err)
		}
	}
	requireRowTenant(t, handle, "a", "acme")

	if err := handle.WithContext(crossContext()).Model(&order{}).
		Where("id = ?", "a").Update("tenant_id", "globex").Error; err != nil {
		t.Fatalf("a cross-tenant update moving a row on purpose: %v", err)
	}
	requireRowTenant(t, handle, "a", "globex")
}

type readOnlyTenantOrder struct {
	tenant.Model
	TenantID *string `gorm:"column:tenant_id;->"`
	ID       string  `gorm:"column:id;primaryKey"`
	Note     string  `gorm:"column:note"`
}

// TestAnInsertLeavingTheTenantColumnOutIsRefused checks that an insert GORM
// builds without the tenant column is refused and nothing is written.
func TestAnInsertLeavingTheTenantColumnOutIsRefused(t *testing.T) {
	handle := open(t)
	if err := handle.Exec("CREATE TABLE loose (tenant_id TEXT NULL, id TEXT NOT NULL, note TEXT)").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	acme := func() *gorm.DB { return handle.WithContext(scopedContext("acme")).Table("loose") }
	across := func() *gorm.DB { return handle.WithContext(crossContext()).Table("loose") }
	for name, write := range map[string]func() error{
		"a selection naming other columns": func() error {
			return acme().Select("ID", "Note").Create(&order{ID: "x", Note: "n"}).Error
		},
		"an omission of the column": func() error {
			return acme().Omit("tenant_id").Create(&order{ID: "x", Note: "n"}).Error
		},
		"a map under a selection naming other columns": func() error {
			return acme().Model(&order{}).Select("id", "note").
				Create(map[string]any{"id": "x", "note": "n"}).Error
		},
		"a map naming the column under a selection leaving it out": func() error {
			return acme().Model(&order{}).Select("id").
				Create(map[string]any{"id": "x", "tenant_id": "acme"}).Error
		},
		"a field GORM only reads": func() error {
			return acme().Create(&readOnlyTenantOrder{ID: "x", Note: "n"}).Error
		},
		"across tenants, a row naming its tenant under a selection leaving it out": func() error {
			return across().Select("ID", "Note").
				Create(&order{Model: tenant.Model{TenantID: "globex"}, ID: "x"}).Error
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := noPanic(t, write)
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("an insert leaving the tenant column out gave %v, want ErrNoTenantInContext", err)
			}
			var n int64
			if err := handle.WithContext(crossContext()).Raw("SELECT count(*) FROM loose").Scan(&n).Error; err != nil {
				t.Fatalf("counting: %v", err)
			}
			if n != 0 {
				t.Fatalf("a refused insert left %d rows", n)
			}
		})
	}

	t.Run("a selection naming the column", func(t *testing.T) {
		if err := acme().Select("ID", "Note", "TenantID").Create(&order{ID: "y", Note: "n"}).Error; err != nil {
			t.Fatalf("an insert selecting the tenant column: %v", err)
		}
		var owner *string
		if err := handle.WithContext(crossContext()).Raw("SELECT tenant_id FROM loose WHERE id = 'y'").
			Scan(&owner).Error; err != nil || owner == nil || *owner != "acme" {
			t.Fatalf("the row selecting the tenant column belongs to %v (%v), want acme", owner, err)
		}
	})
}
