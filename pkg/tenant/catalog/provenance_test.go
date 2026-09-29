package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about a condition the caller writes on the tenant
// column itself. The filter judges it and never rewrites it: the statement
// reaches the rows the condition selects as written, within the context's
// tenant, and a write whose condition names another tenant may be refused
// instead. Filling a tenant half belongs to a key — the shape GORM's
// association mode writes out of a record, whoever wrote it — and to no other
// shape of condition.

// provOrder is the scoped model the caller's conditions are written against.
type provOrder struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (provOrder) TableName() string { return "prov_orders" }

// provSeed is every row of the table, each written as id@tenant: two rows of
// acme's, and globex holding one under an identifier acme also uses.
var provSeed = []string{"a1@acme", "a2@acme", "a1@globex", "b@globex"}

// provShape is one way a caller writes a condition on the tenant column,
// given the value it compares the column with.
type provShape struct {
	name  string
	where func(tx *gorm.DB, v any) *gorm.DB
	// skipsZero marks a shape GORM leaves out when the value is empty: a
	// struct's zero field, which then carries no condition at all.
	skipsZero bool
	// typed marks a shape that binds the value as the struct's field holds
	// it, which is an identifier whatever the caller wrote.
	typed bool
	// judged marks a shape the filter reads as a condition on the column, so
	// that a write naming another tenant through it may be refused.
	judged bool
	// key marks the shape GORM's association mode writes a record's key in: a
	// tuple over the whole key, each column qualified by the table's name, with
	// the rest of it written. Its tenant half is the one the filter fills where
	// it is empty.
	key bool
	// selects reports whether the condition, as written, selects a row of the
	// tenant owner under the identifier id, given the tenant its value names;
	// nil where it selects the rows of that tenant.
	selects func(owner, id, names string) bool
}

var provShapes = []provShape{
	{
		name: "a tuple over the whole key qualified by the table's name", key: true, judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Where(clause.IN{Column: provNamed("tenant_id", "id"), Values: []any{[]any{v, "a1"}}})
		},
		selects: func(owner, id, names string) bool { return owner == names && id == "a1" },
	},
	{
		name: "a tuple over the whole key", judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Where(clause.IN{
				Column: []clause.Column{{Name: "tenant_id"}, {Name: "id"}},
				Values: []any{[]any{v, "a1"}},
			})
		},
		selects: func(owner, id, names string) bool { return owner == names && id == "a1" },
	},
	{
		name: "a tuple over the whole key qualified by the current-table placeholder", judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Where(clause.IN{
				Column: []clause.Column{
					{Table: clause.CurrentTable, Name: "tenant_id"}, {Table: clause.CurrentTable, Name: "id"},
				},
				Values: []any{[]any{v, "a1"}},
			})
		},
		selects: func(owner, id, names string) bool { return owner == names && id == "a1" },
	},
	{
		name: "a tuple over the whole key qualified by the table's name with the rest of it empty", judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Where(clause.IN{Column: provNamed("tenant_id", "id"), Values: []any{[]any{v, ""}}})
		},
		selects: func(string, string, string) bool { return false },
	},
	{name: "a map", judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(map[string]any{"tenant_id": v})
	}},
	{name: "a map listing values", judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(map[string]any{"tenant_id": []any{v}})
	}},
	{name: "a struct", skipsZero: true, typed: true, judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(&provOrder{Model: tenant.Model{TenantID: tenant.ID(fmt.Sprint(v))}})
	}},
	{name: "a struct selecting the column", typed: true, judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(&provOrder{Model: tenant.Model{TenantID: tenant.ID(fmt.Sprint(v))}}, "tenant_id")
	}},
	{name: "a column name and a value", where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where("tenant_id", v)
	}},
	{name: "a string", where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where("tenant_id = ?", v)
	}},
	{name: "a string listing values", where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where("tenant_id IN ?", []any{v})
	}},
	{name: "an equality", judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(clause.Eq{Column: clause.Column{Name: "tenant_id"}, Value: v})
	}},
	{name: "an equality on the qualified column", judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(clause.Eq{Column: clause.Column{Table: clause.CurrentTable, Name: "tenant_id"}, Value: v})
	}},
	{name: "an IN", judged: true, where: func(tx *gorm.DB, v any) *gorm.DB {
		return tx.Where(clause.IN{Column: clause.Column{Name: "tenant_id"}, Values: []any{v}})
	}},
	{
		name: "a NOT around an equality", judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Not(clause.Eq{Column: clause.Column{Name: "tenant_id"}, Value: v})
		},
		selects: func(owner, _, names string) bool { return owner != names },
	},
	{
		name: "an OR of equalities", judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Where(clause.Or(
				clause.Eq{Column: clause.Column{Name: "tenant_id"}, Value: v},
				clause.Eq{Column: clause.Column{Name: "id"}, Value: "none"},
			))
		},
		selects: func(owner, id, names string) bool { return owner == names || id == "none" },
	},
	{
		name: "a tuple qualified by the table's name over the tenant column and a column outside any key", judged: true,
		where: func(tx *gorm.DB, v any) *gorm.DB {
			return tx.Where(clause.IN{Column: provNamed("tenant_id", "note"), Values: []any{[]any{v, "x"}}})
		},
	},
}

// provNamed is columns of the table, each qualified by its name, as GORM's
// association mode writes the columns of a key.
func provNamed(names ...string) []clause.Column {
	cols := make([]clause.Column, 0, len(names))
	for _, name := range names {
		cols = append(cols, clause.Column{Table: "prov_orders", Name: name})
	}
	return cols
}

// provValue is one value a caller compares the tenant column with.
type provValue struct {
	name string
	v    any
	// names is the tenant the value names, empty for an empty value.
	names string
}

var provValues = []provValue{
	{name: "an empty string", v: ""},
	{name: "an empty identifier", v: tenant.ID("")},
	{name: "another tenant's name", v: "globex", names: "globex"},
	{name: "another tenant's identifier", v: tenant.ID("globex"), names: "globex"},
	{name: "the context's name", v: "acme", names: "acme"},
	{name: "the context's identifier", v: tenant.ID("acme"), names: "acme"},
}

// provContext is one context a statement runs under.
type provContext struct {
	name string
	ctx  func() context.Context
	// tenant is the tenant the context carries, empty across tenants or with
	// none.
	tenant string
	cross  bool
}

var provContexts = []provContext{
	{name: "acme", ctx: func() context.Context { return scopedContext("acme") }, tenant: "acme"},
	{name: "across tenants", ctx: crossContext, cross: true},
	{name: "no tenant", ctx: context.Background},
}

// provStatements are the statements each condition is put on. Each returns
// the rows it reached, written as id@tenant, or its error.
var provStatements = []struct {
	name  string
	write bool
	run   func(t *testing.T, handle, scoped *gorm.DB) (string, error)
}{
	{name: "Find", run: func(t *testing.T, _, scoped *gorm.DB) (string, error) {
		var rows []provOrder
		err := scoped.Find(&rows).Error
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ID+"@"+string(r.TenantID))
		}
		return provList(out), err
	}},
	{name: "Count", run: func(t *testing.T, _, scoped *gorm.DB) (string, error) {
		var n int64
		err := scoped.Model(&provOrder{}).Count(&n).Error
		return fmt.Sprint(n), err
	}},
	{name: "Delete", write: true, run: func(t *testing.T, handle, scoped *gorm.DB) (string, error) {
		err := scoped.Delete(&provOrder{}).Error
		left := provRows(t, handle, "")
		var gone []string
		for _, row := range provSeed {
			if !slices.Contains(left, row) {
				gone = append(gone, row)
			}
		}
		return provList(gone), err
	}},
	{name: "Update", write: true, run: func(t *testing.T, handle, scoped *gorm.DB) (string, error) {
		err := scoped.Model(&provOrder{}).Update("note", "touched").Error
		return provList(provRows(t, handle, "touched")), err
	}},
}

// Every shape a caller writes a condition on the tenant column in, with every
// value, is judged as written on both dialects: under a tenant the statement
// reaches what the condition selects within that tenant — nothing for an empty
// value, which an identifier refuses to be bound as — and a write carrying no
// condition of the caller's at all is refused, as GORM refuses one. Across
// tenants the condition is the whole of it, and with no tenant every statement
// is refused. Nothing else is filled into the caller's condition, so an empty
// value never widens into the context's tenant. The one shape filled is the
// one GORM writes a record's key in, a tuple over the whole key with each
// column qualified by the table's name and the rest of it written, and it
// reaches the row its key names in the context's tenant and no other.
func TestACallersConditionOnTheTenantColumnIsJudgedAsWritten(t *testing.T) {
	for _, d := range []struct {
		name string
		open func(*testing.T) *gorm.DB
	}{
		{"sqlite", openShapesOnSQLite},
		{"postgres", openShapesOnPostgres},
	} {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			if err := handle.Exec("CREATE TABLE prov_orders (tenant_id TEXT NOT NULL, id TEXT NOT NULL, " +
				"note TEXT, PRIMARY KEY (tenant_id, id))").Error; err != nil {
				t.Fatalf("creating the table: %v", err)
			}
			for _, shape := range provShapes {
				for _, value := range provValues {
					for _, c := range provContexts {
						for _, stmt := range provStatements {
							provSeedRows(t, handle)
							scoped := shape.where(handle.WithContext(c.ctx()), value.v)
							got, err := stmt.run(t, handle, scoped)
							want, refusal := provExpected(shape, value, c, stmt.write, stmt.name == "Count")
							cell := fmt.Sprintf("%s with %s under %s, %s", shape.name, value.name, c.name, stmt.name)
							t.Logf("%s: %s", cell, provOutcome(got, err))
							switch {
							case refusal != nil:
								if !errors.Is(err, refusal) {
									t.Errorf("%s gave %s, want %v", cell, provOutcome(got, err), refusal)
								}
							case stmt.write && shape.judged && value.names != "" && value.names != c.tenant &&
								!c.cross && errors.Is(err, tenant.ErrExplicitTenantMismatch):
								// A write whose condition names another tenant may be
								// refused rather than reach nothing.
								if got != "" {
									t.Errorf("%s was refused and still reached %s", cell, got)
								}
							case err != nil || got != want:
								t.Errorf("%s gave %s, want %q", cell, provOutcome(got, err), want)
							}
						}
					}
				}
			}
		})
	}
}

// provExpected is what a statement owes: the rows its condition selects as
// written, within the context's tenant, or the refusal it is owed instead. A
// key's empty tenant half is the one thing filled, and only under a tenant: the
// key then selects the row it names in that tenant.
func provExpected(shape provShape, value provValue, c provContext, write, count bool) (string, error) {
	if !c.cross && c.tenant == "" {
		return "", tenant.ErrNoTenantInContext
	}
	names := value.names
	filled := shape.key && names == "" && !c.cross
	if filled {
		names = c.tenant
	}
	empty := names == ""
	conditioned := !empty || !shape.skipsZero
	if !conditioned && write {
		return "", gorm.ErrMissingWhereClause
	}
	_, identifier := value.v.(tenant.ID)
	if conditioned && empty && (shape.typed || identifier) {
		// An identifier refuses to be bound empty, on any handle.
		return "", tenant.ErrNoTenantInContext
	}
	selects := shape.selects
	if selects == nil {
		selects = func(owner, _, names string) bool { return owner == names }
	}
	var rows []string
	for _, row := range provSeed {
		id, owner, _ := strings.Cut(row, "@")
		if !c.cross && owner != c.tenant {
			continue
		}
		if !conditioned || selects(owner, id, names) {
			rows = append(rows, row)
		}
	}
	if count {
		return fmt.Sprint(len(rows)), nil
	}
	return provList(rows), nil
}

// provSeedRows writes the seed into the table, past the filter.
func provSeedRows(t *testing.T, handle *gorm.DB) {
	t.Helper()
	cross := handle.WithContext(crossContext())
	if err := cross.Exec("DELETE FROM prov_orders").Error; err != nil {
		t.Fatalf("emptying the table: %v", err)
	}
	if err := cross.Exec("INSERT INTO prov_orders (tenant_id, id, note) VALUES ('acme', 'a1', 'x'), " +
		"('acme', 'a2', 'x'), ('globex', 'a1', 'x'), ('globex', 'b', 'x')").Error; err != nil {
		t.Fatalf("seeding the table: %v", err)
	}
}

// provRows reads the table back past the filter, each row as id@tenant: every
// row, or those carrying the note given.
func provRows(t *testing.T, handle *gorm.DB, note string) []string {
	t.Helper()
	var rows []provOrder
	q := handle.WithContext(crossContext())
	if note != "" {
		q = q.Where("note = ?", note)
	}
	if err := q.Find(&rows).Error; err != nil {
		t.Fatalf("reading the table back: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID+"@"+string(r.TenantID))
	}
	return out
}

// provList writes rows in one order.
func provList(rows []string) string {
	sorted := slices.Clone(rows)
	slices.Sort(sorted)
	return strings.Join(sorted, ",")
}

// provOutcome is a statement's outcome as a case reports it.
func provOutcome(got string, err error) string {
	if err != nil {
		return fmt.Sprintf("%q and %v", got, err)
	}
	return fmt.Sprintf("%q", got)
}
