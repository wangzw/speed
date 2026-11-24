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

type provOrder struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (provOrder) TableName() string { return "prov_orders" }

var provSeed = []string{"a1@acme", "a2@acme", "a1@globex", "b@globex"}

type provShape struct {
	name      string
	where     func(tx *gorm.DB, v any) *gorm.DB
	skipsZero bool
	typed     bool
	judged    bool
	key       bool
	selects   func(owner, id, names string) bool
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

func provNamed(names ...string) []clause.Column {
	cols := make([]clause.Column, 0, len(names))
	for _, name := range names {
		cols = append(cols, clause.Column{Table: "prov_orders", Name: name})
	}
	return cols
}

type provValue struct {
	name  string
	v     any
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

type provContext struct {
	name   string
	ctx    func() context.Context
	tenant string
	cross  bool
}

var provContexts = []provContext{
	{name: "acme", ctx: func() context.Context { return scopedContext("acme") }, tenant: "acme"},
	{name: "across tenants", ctx: crossContext, cross: true},
	{name: "no tenant", ctx: context.Background},
}

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

func TestACallersConditionOnTheTenantColumnIsJudgedAsWritten(t *testing.T) {
	for _, d := range shapeDialects {
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

func provList(rows []string) string {
	sorted := slices.Clone(rows)
	slices.Sort(sorted)
	return strings.Join(sorted, ",")
}

func provOutcome(got string, err error) string {
	if err != nil {
		return fmt.Sprintf("%q and %v", got, err)
	}
	return fmt.Sprintf("%q", got)
}
