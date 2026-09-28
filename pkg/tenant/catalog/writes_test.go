package catalog

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about what a write puts into the tenant column:
// every shape GORM accepts an assignment or a row in has to be read the way
// GORM reads it, so that a value naming another tenant — or no tenant at all —
// cannot reach the column through a shape the filter does not look at.

// requireNoRow asserts that no row carries this identifier.
func requireNoRow(t *testing.T, handle *gorm.DB, id string) {
	t.Helper()
	if got := tenantsOf(t, handle)[id]; len(got) != 0 {
		t.Fatalf("a refused write left rows identified by %s, belonging to %q", id, got)
	}
}

// noPanic runs a statement and turns a panic into a failure of the case, so
// that one shape panicking does not end the whole binary.
func noPanic(t *testing.T, run func() error) (err error) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("the statement panicked: %v", p)
		}
	}()
	return run()
}

// The tenant column of an update is found the way GORM finds it, so every key
// GORM resolves to that column is one the check has to read.
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

// A value the check cannot read as the context's identifier is not one it can
// let through: an expression, and the empty value, which leaves the row to no
// tenant at all.
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

// Writing the context's own identifier is the same statement said twice.
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

// A struct update selecting the tenant column writes it even at its zero value.
func TestUpdateSelectingAnUnwrittenTenantColumnIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	err := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where("id = ?", "a").Select("*").Updates(order{Note: "touched"}).Error
	requireMismatch(t, err)
	requireRowTenant(t, handle, "a", "acme")
}

// A save rewrites every column of the row it is handed, and its key is a
// condition rather than an assignment, so a row read back and saved is not a
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

// An explicit SET clause is the assignment list GORM renders as it stands.
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

// An update carrying its assignments in a type other than the model's is how a
// PATCH handler usually writes one.
type orderPatch struct {
	Note string `gorm:"column:note"`
}

type orderPatchWithTenant struct {
	TenantID string `gorm:"column:tenant_id"`
	Note     string `gorm:"column:note"`
}

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

// GORM accepts an insert handed by value, as a slice of values and as a slice
// of pointers; each one has to land in the context's tenant.
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

func TestCreateByValueNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)
	err := handle.WithContext(scopedContext("acme")).
		Create([]*order{{ID: "w0"}, {Model: tenant.Model{TenantID: "globex"}, ID: "w1"}}).Error
	requireMismatch(t, err)
	requireNoRow(t, handle, "w0")
	requireNoRow(t, handle, "w1")
}

// A map insert names its columns by key, and GORM resolves the keys the same
// way it resolves an update's.
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

// The assignments of an upsert run on the conflicting row, which is the
// context's own: assigning the tenant column there moves that row away.
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

// An upsert that reassigns the column from the row it was about to insert is
// the same statement written twice, and the inserted row carries the context's
// tenant.
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

// Crossing tenants lifts the condition and the fill, not the column: a row
// written across tenants with no tenant in its column belongs to none, and no
// scoped statement can ever reach it again.
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
