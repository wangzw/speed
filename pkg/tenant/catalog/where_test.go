package catalog

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/tenant"
)

// GORM refuses an update or a delete that carries no condition at all.
func TestAnUnconditionedUpdateIsStillRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Model(&order{}).Update("note", "wiped").Error
	if !errors.Is(err, gorm.ErrMissingWhereClause) {
		t.Fatalf("an unconditioned update gave %v, want gorm.ErrMissingWhereClause", err)
	}
	var notes []string
	if err := handle.WithContext(crossContext()).Model(&order{}).Order("id").
		Pluck("note", &notes).Error; err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if len(notes) != 2 || notes[0] != "for acme" || notes[1] != "for globex" {
		t.Fatalf("after a refused update the notes are %v", notes)
	}
}

// TestAnUnconditionedDeleteIsStillRefused checks that an unconditioned delete is
// still refused.
func TestAnUnconditionedDeleteIsStillRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Delete(&order{}).Error
	if !errors.Is(err, gorm.ErrMissingWhereClause) {
		t.Fatalf("an unconditioned delete gave %v, want gorm.ErrMissingWhereClause", err)
	}
	requireRowTenant(t, handle, "a", "acme")
}

type softOrder struct {
	tenant.Model
	ID        string         `gorm:"column:id;primaryKey"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at"`
}

func (softOrder) TableName() string { return "soft_orders" }

// TestAnUnconditionedSoftDeleteIsStillRefused checks that an unconditioned soft
// delete is still refused.
func TestAnUnconditionedSoftDeleteIsStillRefused(t *testing.T) {
	handle := open(t)
	if err := handle.Exec("CREATE TABLE soft_orders (tenant_id TEXT NOT NULL, id TEXT NOT NULL, " +
		"deleted_at DATETIME, PRIMARY KEY (tenant_id, id))").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := handle.WithContext(crossContext()).Exec(
		"INSERT INTO soft_orders (tenant_id, id) VALUES ('acme', 'a'), ('globex', 'b')").Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}
	ctx := scopedContext("acme")

	err := handle.WithContext(ctx).Delete(&softOrder{}).Error
	if !errors.Is(err, gorm.ErrMissingWhereClause) {
		t.Fatalf("an unconditioned soft delete gave %v, want gorm.ErrMissingWhereClause", err)
	}
	var live int64
	if err := handle.WithContext(crossContext()).Model(&softOrder{}).Count(&live).Error; err != nil {
		t.Fatalf("counting: %v", err)
	}
	if live != 2 {
		t.Fatalf("after a refused soft delete %d rows are live, want 2", live)
	}

	res := handle.WithContext(ctx).Where("id <> ''").Delete(&softOrder{})
	if res.Error != nil || res.RowsAffected != 1 {
		t.Fatalf("a conditioned soft delete gave %v and reached %d rows, want acme's one",
			res.Error, res.RowsAffected)
	}
}

// An update a caller deliberately widened with AllowGlobalUpdate is still one
// tenant's statement.
func TestAGlobalUpdateStaysInsideTheTenant(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	res := handle.WithContext(scopedContext("acme")).Session(&gorm.Session{AllowGlobalUpdate: true}).
		Model(&order{}).Update("note", "everything of acme")
	if res.Error != nil {
		t.Fatalf("a deliberately global update: %v", res.Error)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("a global update reached %d rows, want acme's one", res.RowsAffected)
	}
}

func seedSharedID(t *testing.T, handle *gorm.DB) {
	t.Helper()
	seed(t, handle)
	if err := handle.WithContext(crossContext()).Exec(
		"INSERT INTO orders (id, tenant_id, note) VALUES ('a', 'globex', 'globex also has an a')").
		Error; err != nil {
		t.Fatalf("seeding a second row a: %v", err)
	}
}

// A delete addressed by the struct's own key is how GORM deletes one row; under
// the composite key the tenant half of that key is the context's.
func TestDeleteByTheStructKeyReachesTheContextsRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		dest func() any
	}{
		{"a pointer", func() any { return &order{ID: "a"} }},
		{"a value", func() any { return order{ID: "a"} }},
		{"a slice", func() any { return &[]order{{ID: "a"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := open(t)
			seedSharedID(t, handle)

			res := handle.WithContext(scopedContext("acme")).Delete(tc.dest())
			if res.Error != nil {
				t.Fatalf("deleting: %v", res.Error)
			}
			if res.RowsAffected != 1 {
				t.Fatalf("a delete by key reached %d rows, want acme's one", res.RowsAffected)
			}
			requireRowTenant(t, handle, "a", "globex")
		})
	}
}

// A key whose tenant half names another tenant is a write naming that tenant.
func TestDeleteByAKeyNamingAnotherTenantIsRefused(t *testing.T) {
	handle := open(t)
	seedSharedID(t, handle)

	err := handle.WithContext(scopedContext("acme")).
		Delete(&order{Model: tenant.Model{TenantID: "globex"}, ID: "a"}).Error
	requireMismatch(t, err)
	if got := tenantsOf(t, handle)["a"]; len(got) != 2 {
		t.Fatalf("a refused delete left the rows identified by a with %q, want both", got)
	}
}

// An update addressed by a batch of keys renders them as tuples, whose tenant
// half is the context's too.
func TestUpdateByABatchOfKeysReachesTheContextsRows(t *testing.T) {
	handle := open(t)
	seedSharedID(t, handle)

	res := handle.WithContext(scopedContext("acme")).Model(&[]order{{ID: "a"}}).Update("note", "touched")
	if res.Error != nil {
		t.Fatalf("updating: %v", res.Error)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("an update by keys reached %d rows, want acme's one", res.RowsAffected)
	}
}

// FirstOrCreate and FirstOrInit copy the conditions they searched by onto the
// value they return when nothing was found.
func TestFirstOrCreateKeepsTheConditionsItSearchedBy(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	ctx := scopedContext("acme")

	var byConds order
	if err := handle.WithContext(ctx).FirstOrCreate(&byConds, order{ID: "n1"}).Error; err != nil {
		t.Fatalf("FirstOrCreate by conditions: %v", err)
	}
	if byConds.ID != "n1" {
		t.Fatalf("FirstOrCreate by conditions returned id %q, want n1", byConds.ID)
	}
	requireRowTenant(t, handle, "n1", "acme")

	var byWhere order
	if err := handle.WithContext(ctx).Where(order{ID: "n2"}).FirstOrCreate(&byWhere).Error; err != nil {
		t.Fatalf("FirstOrCreate by a where: %v", err)
	}
	if byWhere.ID != "n2" {
		t.Fatalf("FirstOrCreate by a where returned id %q, want n2", byWhere.ID)
	}
	requireRowTenant(t, handle, "n2", "acme")

	var inited order
	if err := handle.WithContext(ctx).Where("id = ?", "n3").
		FirstOrInit(&inited, order{Note: "init"}).Error; err != nil {
		t.Fatalf("FirstOrInit: %v", err)
	}
	if inited.Note != "init" {
		t.Fatalf("FirstOrInit returned %+v, want the condition's note", inited)
	}

	var other order
	if err := handle.WithContext(ctx).FirstOrCreate(&other, order{ID: "b"}).Error; err != nil {
		t.Fatalf("FirstOrCreate over another tenant's id: %v", err)
	}
	if got := tenantsOf(t, handle)["b"]; len(got) != 2 {
		t.Fatalf("the rows identified by b belong to %q, want both tenants to hold one", got)
	}
}

// GORM renders a leading single OR by moving it behind the first expression that is
// not one.
func TestALeadingOrKeepsTheMeaningGORMGivesIt(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []order
	if err := handle.WithContext(scopedContext("acme")).
		Or("note = ?", "for acme").Where("id = ?", "no-such-id").
		Find(&got).Error; err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("a leading OR returned %v, want acme's row as GORM renders it", got)
	}
}

// A lone OR, and a lone raw condition carrying one, stay inside the tenant.
func TestALoneOrStaysInsideTheTenant(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	for name, q := range map[string]func(*gorm.DB) *gorm.DB{
		"an Or alone": func(tx *gorm.DB) *gorm.DB { return tx.Or("note = ? OR id = ?", "for globex", "b") },
		"a raw OR":    func(tx *gorm.DB) *gorm.DB { return tx.Where("note = ? OR id = ?", "for globex", "b") },
		"a named raw OR": func(tx *gorm.DB) *gorm.DB {
			return tx.Where("note = @n OR id = @i", map[string]any{"n": "for globex", "i": "b"})
		},
		"two ORs and nothing": func(tx *gorm.DB) *gorm.DB { return tx.Or("id = ?", "b").Or("note = ?", "for globex") },
	} {
		var got []order
		if err := q(handle.WithContext(scopedContext("acme"))).Find(&got).Error; err != nil {
			t.Fatalf("%s: reading: %v", name, err)
		}
		if len(got) != 0 {
			t.Fatalf("%s escaped the tenant: got %v", name, got)
		}
	}
}

// Statement reuse: a chain executed twice keeps the caller's conditions as the
// caller wrote them, and the second execution is as scoped as the first.
func TestAChainExecutedTwiceStaysScoped(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	q := handle.WithContext(scopedContext("acme")).Model(&order{}).Where("note <> ''")
	var n int64
	if err := q.Count(&n).Error; err != nil {
		t.Fatalf("counting: %v", err)
	}
	var got []order
	if err := q.Find(&got).Error; err != nil {
		t.Fatalf("reading: %v", err)
	}
	if n != 1 || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("a reused chain counted %d and read %v, want acme's one row both times", n, got)
	}
}

// A chain executed under one tenant and then carried to another context is
// judged by the context it runs under now, not by the one it first ran in.
func TestAChainCarriedToAnotherContextIsJudgedAfresh(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	q := handle.WithContext(scopedContext("acme")).Model(&order{}).Where("note <> ''")
	var first []order
	if err := q.Find(&first).Error; err != nil {
		t.Fatalf("reading under acme: %v", err)
	}

	var globex []order
	if err := q.WithContext(scopedContext("globex")).Find(&globex).Error; err != nil {
		t.Fatalf("reading under globex: %v", err)
	}
	if len(globex) != 1 || globex[0].ID != "b" {
		t.Fatalf("a chain carried to globex read %v, want globex's row", globex)
	}

	var all []order
	if err := q.WithContext(crossContext()).Find(&all).Error; err != nil {
		t.Fatalf("reading across tenants: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("a chain carried across tenants read %v, want both rows", all)
	}

	var none []order
	err := q.WithContext(context.Background()).Find(&none).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("a chain carried to no tenant gave %v, want ErrNoTenantInContext", err)
	}
}

func byKey(id string) clause.IN {
	return clause.IN{
		Column: []clause.Column{{Table: "orders", Name: "tenant_id"}, {Table: "orders", Name: "id"}},
		Values: []any{[]any{tenant.ID(""), id}},
	}
}

// A key filled under one tenant is filled afresh under the next, so a chain
// carried to another context addresses that tenant's row.
func TestAKeyFilledUnderOneTenantIsFilledAfreshUnderAnother(t *testing.T) {
	handle := open(t)
	seedSharedID(t, handle)

	q := handle.WithContext(scopedContext("acme")).Model(&order{}).Where(byKey("a"))
	var first []order
	if err := q.Find(&first).Error; err != nil || len(first) != 1 || first[0].TenantID != "acme" {
		t.Fatalf("a read by key under acme gave %v and %v, want acme's a", err, first)
	}
	var second []order
	if err := q.WithContext(scopedContext("globex")).Find(&second).Error; err != nil ||
		len(second) != 1 || second[0].TenantID != "globex" {
		t.Fatalf("the same read carried to globex gave %v and %v, want globex's a", err, second)
	}
}

// A column qualified by another table belongs to that table, so a tuple over
// it is no key of the statement's own.
func TestATupleOverAnotherTablesColumnIsNoKey(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	if err := handle.Exec("INSERT INTO memos (id, tenant_id, note) VALUES ('a', 'acme', 'for acme')").
		Error; err != nil {
		t.Fatalf("seeding a memo: %v", err)
	}
	joined := func() *gorm.DB {
		return handle.WithContext(scopedContext("acme")).Model(&order{}).Joins("JOIN memos ON memos.id = orders.id")
	}

	var n int64
	if err := joined().Where(clause.IN{
		Column: []clause.Column{{Table: "orders", Name: "tenant_id"}, {Table: "memos", Name: "id"}},
		Values: []any{[]any{"", "a"}},
	}).Count(&n).Error; err != nil || n != 0 {
		t.Fatalf("a read by the order's tenant and the memo's identifier, the tenant empty, gave %v and %d, "+
			"want none", err, n)
	}
	if err := joined().Where(byKey("a")).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("the same read keyed on the order's own columns gave %v and %d, want acme's a", err, n)
	}
}

// A write conditioned on another table's tenant column names no tenant of the statement's own: the
// filter leaves the condition to the database, which refuses a column the statement cannot reach.
func TestAWriteConditionedOnAnotherTablesTenantColumnIsNotJudged(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	err := handle.WithContext(scopedContext("acme")).Model(&order{}).
		Where(clause.Eq{Column: clause.Column{Table: "memos", Name: "tenant_id"}, Value: "globex"}).
		Update("note", "touched").Error
	if err == nil || errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Fatalf("an update conditioned on the memos' tenant column gave %v, want the database's own refusal", err)
	}
	requireRowTenant(t, handle, "a", "acme")
}
