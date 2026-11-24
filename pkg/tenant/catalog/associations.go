package catalog

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormschema "gorm.io/gorm/schema"

	"github.com/vislake/speed/pkg/tenant"
)

func tenantFieldOf(s *gormschema.Schema) (*gormschema.Field, error) {
	if s == nil {
		return nil, nil
	}
	if joined := joinedBy(s); len(joined) > 0 {
		return joinTenantField(s, joined)
	}
	field, err := conventionFieldOf(s)
	if err != nil || field == nil {
		return field, err
	}
	if err := unjudgedRead(s, field); err != nil {
		return nil, err
	}
	return field, nil
}

func unjudgedRead(s *gormschema.Schema, field *gormschema.Field) error {
	for _, m := range s.Relationships.Many2Many {
		if m.Schema != s || m.JoinTable == nil || m.FieldSchema == nil {
			continue
		}
		if other, err := conventionFieldOf(m.FieldSchema); err != nil || other != nil {
			continue
		}
		switch {
		case linkColumn(m, true, field) == "":
			return fmt.Errorf("%w: model %s declares %s, a many-to-many association with %s, a global model, "+
				"through join table %s, and joins it by its key without its tenant column: the read of the "+
				"association joins that table by name and names no tenant, so it reaches the links of every "+
				"tenant holding the key; join by the whole key, the tenant column included",
				tenant.ErrNoTenantInContext, s.Name, m.Name, m.FieldSchema.Name, m.JoinTable.Table)
		case !identifierField(field):
			return fmt.Errorf("%w: model %s declares %s, a many-to-many association with %s, a global model, "+
				"through join table %s, and binds its tenant column to field %s, which is not a tenant.ID: the "+
				"read of the association joins that table by name and carries the tenant only as that field's "+
				"value, which the filter cannot tell from any other; declare the field as tenant.ID",
				tenant.ErrNoTenantInContext, s.Name, m.Name, m.FieldSchema.Name, m.JoinTable.Table, field.Name)
		}
	}
	return nil
}

func identifierField(f *gormschema.Field) bool {
	t := f.FieldType
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == identifierType
}

// joinedBy reads the schema's relationships under the lock GORM writes them
// under: parsing a has-one or has-many association writes a back reference into
// the associated model.
func joinedBy(s *gormschema.Schema) []*gormschema.Relationship {
	s.Relationships.Mux.RLock()
	sides := make([]*gormschema.Schema, 0, len(s.Relationships.Relations))
	for _, rel := range s.Relationships.Relations {
		if rel.FieldSchema != nil && rel.FieldSchema != s && !slices.Contains(sides, rel.FieldSchema) {
			sides = append(sides, rel.FieldSchema)
		}
	}
	s.Relationships.Mux.RUnlock()
	var out []*gormschema.Relationship
	for _, side := range sides {
		for _, m := range side.Relationships.Many2Many {
			if m.JoinTable == s && !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	slices.SortFunc(out, func(a, b *gormschema.Relationship) int {
		return strings.Compare(a.Schema.Name+"."+a.Name, b.Schema.Name+"."+b.Name)
	})
	return out
}

func joinTenantField(s *gormschema.Schema, joined []*gormschema.Relationship) (*gormschema.Field, error) {
	column := ""
	var scoped []string
	for _, m := range joined {
		for _, side := range []struct {
			schema *gormschema.Schema
			own    bool
		}{{m.Schema, true}, {m.FieldSchema, false}} {
			f, err := conventionFieldOf(side.schema)
			if err != nil {
				return nil, err
			}
			if f == nil {
				continue
			}
			if !slices.Contains(scoped, side.schema.Name) {
				scoped = append(scoped, side.schema.Name)
			}
			carried := linkColumn(m, side.own, f)
			switch {
			case carried == "":
			case column == "":
				column = carried
			case carried != column:
				return nil, fmt.Errorf("%w: join table %s of %s.%s carries the tenants of the models it joins "+
					"on two columns, %s and %s, and a link belongs to one tenant; name one column for both "+
					"with joinForeignKey and joinReferences", tenant.ErrNoTenantInContext, s.Table,
					m.Schema.Name, m.Name, column, carried)
			}
		}
	}
	own, err := conventionFieldOf(s)
	switch {
	case err != nil:
		return nil, err
	case column == "" && len(scoped) > 0:
		return nil, fmt.Errorf("%w: join table %s of %s.%s carries no tenant: it joins %s by keys without "+
			"their tenant column, so a link belongs to no tenant and names a row of every tenant holding "+
			"the key; join by the whole key, the tenant column included", tenant.ErrNoTenantInContext,
			s.Table, joined[0].Schema.Name, joined[0].Name, strings.Join(scoped, " and "))
	case column == "":
		return own, nil
	case own != nil && own.DBName != column:
		return nil, fmt.Errorf("%w: join model %s of table %s embeds tenant.Model on %s, and the models it "+
			"joins put their tenant on %s, and a link belongs to one tenant; embed the convention on the "+
			"column the links carry the tenant in", tenant.ErrNoTenantInContext, s.Name, s.Table, own.DBName,
			column)
	}
	field := s.FieldsByDBName[column]
	if field == nil {
		return nil, fmt.Errorf("%w: join table %s carries the tenant on %s, which its model binds to no "+
			"field", tenant.ErrNoTenantInContext, s.Table, column)
	}
	return field, nil
}

func linkColumn(m *gormschema.Relationship, own bool, tenantField *gormschema.Field) string {
	for _, ref := range m.References {
		if ref.OwnPrimaryKey == own && ref.PrimaryValue == "" && ref.PrimaryKey != nil && ref.ForeignKey != nil &&
			ref.PrimaryKey.DBName == tenantField.DBName {
			return ref.ForeignKey.DBName
		}
	}
	return ""
}

// keyHalves fills the tenant half of a key GORM's association mode wrote into the
// conditions, only where another part of the tuple is written: filling an
// otherwise empty tuple would address the whole tenant. The conditions are copied,
// never written into, so that the same statement run again under another context
// finds them as they were.
type keyHalves struct {
	at    func(clause.Column) bool
	key   func([]clause.Column) bool
	id    string
	named []string
}

func (k *keyHalves) exprs(in []clause.Expression) []clause.Expression {
	if len(in) == 0 {
		return in
	}
	out := make([]clause.Expression, len(in))
	for i, e := range in {
		out[i] = k.expr(e)
	}
	return out
}

func (k *keyHalves) expr(e clause.Expression) clause.Expression {
	switch v := e.(type) {
	case clause.IN:
		return k.in(v)
	case clause.Eq:
		if col, ok := v.Column.(clause.Column); ok && k.at(col) {
			k.judge(v.Value)
		}
		return v
	case clause.Where:
		return clause.Where{Exprs: k.exprs(v.Exprs)}
	case clause.AndConditions:
		return clause.AndConditions{Exprs: k.exprs(v.Exprs)}
	case clause.OrConditions:
		return clause.OrConditions{Exprs: k.exprs(v.Exprs)}
	case clause.NotConditions:
		return clause.NotConditions{Exprs: k.exprs(v.Exprs)}
	}
	return e
}

func (k *keyHalves) in(v clause.IN) clause.IN {
	switch col := v.Column.(type) {
	case clause.Column:
		if k.at(col) {
			for _, x := range v.Values {
				k.judge(x)
			}
		}
	case []clause.Column:
		var halves []int
		for i, c := range col {
			if k.at(c) {
				halves = append(halves, i)
			}
		}
		if len(halves) == 0 {
			return v
		}
		keyed := k.key(col)
		values := make([]interface{}, len(v.Values))
		for i, x := range v.Values {
			values[i] = x
			tuple, ok := x.([]interface{})
			if !ok || len(tuple) != len(col) {
				continue
			}
			for _, h := range halves {
				k.judge(tuple[h])
			}
			if keyed && otherPartWritten(tuple, halves) {
				filled := slices.Clone(tuple)
				for _, h := range halves {
					filled[h] = k.filled(tuple[h])
				}
				values[i] = filled
			}
		}
		v.Values = values
	}
	return v
}

func (k *keyHalves) judge(v any) {
	if s, ok := stringValue(reflect.ValueOf(v)); ok && s != "" && s != k.id {
		k.named = append(k.named, s)
	}
}

func (k *keyHalves) filled(v any) any {
	s, ok := stringValue(reflect.ValueOf(v))
	if !ok || s != "" {
		return v
	}
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return reflect.ValueOf(k.id).Convert(t).Interface()
}

func otherPartWritten(tuple []interface{}, halves []int) bool {
	for i, x := range tuple {
		if slices.Contains(halves, i) {
			continue
		}
		if rv := indirect(reflect.ValueOf(x)); rv.IsValid() && !rv.IsZero() {
			return true
		}
	}
	return false
}

func ownColumn(st *gorm.Statement, field *gormschema.Field) func(clause.Column) bool {
	return func(c clause.Column) bool {
		return ownTable(st, c) && c.Name == field.DBName
	}
}

func ownTable(st *gorm.Statement, c clause.Column) bool {
	return !c.Raw && (c.Table == "" || c.Table == clause.CurrentTable || c.Table == st.Table)
}

func wholeKey(st *gorm.Statement) func([]clause.Column) bool {
	return func(cols []clause.Column) bool {
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			if c.Raw || st.Table == "" || c.Table != st.Table {
				return false
			}
			names = append(names, c.Name)
		}
		slices.Sort(names)
		for _, key := range keyColumns(st.Schema) {
			if slices.Equal(key, names) {
				return true
			}
		}
		return false
	}
}

func keyColumns(s *gormschema.Schema) [][]string {
	if s == nil {
		return nil
	}
	keys := [][]string{slices.Sorted(slices.Values(s.PrimaryFieldDBNames))}
	s.Relationships.Mux.RLock()
	rels := make([]*gormschema.Relationship, 0, len(s.Relationships.Relations))
	for _, rel := range s.Relationships.Relations {
		rels = append(rels, rel)
	}
	s.Relationships.Mux.RUnlock()
	rels = append(rels, joinedBy(s)...)
	for _, rel := range rels {
		for _, own := range []bool{true, false} {
			var names []string
			complete := true
			for _, ref := range rel.References {
				if ref.OwnPrimaryKey != own || ref.PrimaryValue != "" {
					continue
				}
				if ref.ForeignKey == nil || ref.ForeignKey.Schema == nil ||
					ref.ForeignKey.Schema.ModelType != s.ModelType {
					complete = false
					break
				}
				names = append(names, ref.ForeignKey.DBName)
			}
			if complete && len(names) > 0 {
				keys = append(keys, slices.Sorted(slices.Values(names)))
			}
		}
	}
	return keys
}

var identifierType = reflect.TypeOf(tenant.ID(""))

func isIdentifier(v any) bool {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == identifierType
}

type fromJoin struct {
	index   int
	table   string
	column  string
	written []clause.Expression
}

func fromJoins(st *gorm.Statement, field *gormschema.Field) ([]fromJoin, error) {
	c, ok := st.Clauses["FROM"]
	if !ok {
		return nil, nil
	}
	from, ok := c.Expression.(clause.From)
	if !ok {
		return nil, nil
	}
	var out []fromJoin
	for i, j := range from.Joins {
		if j.Expression != nil {
			continue
		}
		table := j.Table.Name
		if j.Table.Alias != "" {
			table = j.Table.Alias
		}
		written := writtenOn(j.ON)
		seen := onReading{st: st, field: field, table: table}
		seen.read(written)
		model := "a model"
		if st.Schema != nil {
			model = st.Schema.Name
		}
		switch {
		case seen.byKey && len(seen.columns) == 0:
			return nil, fmt.Errorf("%w: model %s is joined to %s, which carries no tenant, by its key without "+
				"its tenant column, so the join reaches the rows of every tenant holding that key; join by the "+
				"whole key, the tenant column included", tenant.ErrNoTenantInContext, model, table)
		case len(seen.columns) > 1:
			return nil, fmt.Errorf("%w: a read of %s joins %s, which carries tenants on two columns, %s and %s, "+
				"and a row of it belongs to one tenant", tenant.ErrNoTenantInContext, model, table,
				seen.columns[0], seen.columns[1])
		case len(seen.columns) == 1:
			out = append(out, fromJoin{index: i, table: table, column: seen.columns[0], written: written})
		}
	}
	return out, nil
}

type onReading struct {
	st      *gorm.Statement
	field   *gormschema.Field
	table   string
	columns []string
	byKey   bool
}

func (o *onReading) read(exprs []clause.Expression) {
	for _, e := range exprs {
		switch v := e.(type) {
		case clause.IN:
			o.readIN(v)
		case clause.Eq:
			o.readEq(v)
		case clause.Where:
			o.read(v.Exprs)
		case clause.AndConditions:
			o.read(v.Exprs)
		case clause.OrConditions:
			o.read(v.Exprs)
		case clause.NotConditions:
			o.read(v.Exprs)
		}
	}
}

func (o *onReading) readIN(v clause.IN) {
	switch col := v.Column.(type) {
	case clause.Column:
		for _, x := range v.Values {
			if isIdentifier(x) {
				o.carries(col)
			}
		}
	case []clause.Column:
		for _, x := range v.Values {
			tuple, ok := x.([]interface{})
			if !ok || len(tuple) != len(col) {
				continue
			}
			for i, part := range tuple {
				if isIdentifier(part) {
					o.carries(col[i])
				}
			}
		}
	}
}

func (o *onReading) readEq(v clause.Eq) {
	col, ok := v.Column.(clause.Column)
	if !ok || col.Raw || col.Table != o.table {
		return
	}
	if isIdentifier(v.Value) {
		o.carries(col)
		return
	}
	other, ok := v.Value.(clause.Column)
	if !ok || other.Raw || o.st.Schema == nil || o.field == nil ||
		(other.Table != o.st.Table && other.Table != clause.CurrentTable) {
		return
	}
	switch {
	case other.Name == o.field.DBName:
		o.carries(col)
	case slices.Contains(o.st.Schema.PrimaryFieldDBNames, other.Name):
		o.byKey = true
	}
}

func (o *onReading) carries(col clause.Column) {
	if !col.Raw && col.Table == o.table && !slices.Contains(o.columns, col.Name) {
		o.columns = append(o.columns, col.Name)
	}
}

type keyedOn struct {
	written  []clause.Expression
	rendered []clause.Expression
}

// Build renders the conditions as the ON of the join renders its own.
func (k keyedOn) Build(builder clause.Builder) {
	clause.Where{Exprs: slices.Clone(k.rendered)}.Build(builder)
}

func writtenOn(on clause.Where) []clause.Expression {
	if len(on.Exprs) == 1 {
		if k, ours := on.Exprs[0].(keyedOn); ours {
			return k.written
		}
	}
	return on.Exprs
}

func applyFromJoins(st *gorm.Statement, joins []fromJoin, id string) {
	if len(joins) == 0 {
		return
	}
	c := st.Clauses["FROM"]
	from, _ := c.Expression.(clause.From)
	rewritten := slices.Clone(from.Joins)
	for _, fj := range joins {
		halves := keyHalves{
			id: id,
			at: func(col clause.Column) bool {
				return !col.Raw && col.Table == fj.table && col.Name == fj.column
			},
			key: func(cols []clause.Column) bool {
				for _, col := range cols {
					if col.Raw || col.Table != fj.table {
						return false
					}
				}
				return true
			},
		}
		rendered := append(halves.exprs(fj.written), clause.Eq{
			Column: clause.Column{Table: fj.table, Name: fj.column}, Value: id,
		})
		rewritten[fj.index].ON = clause.Where{Exprs: []clause.Expression{keyedOn{written: fj.written, rendered: rendered}}}
	}
	from.Joins = rewritten
	c.Expression = from
	st.Clauses["FROM"] = c
}

func releaseFromJoins(st *gorm.Statement) {
	c, ok := st.Clauses["FROM"]
	if !ok {
		return
	}
	from, ok := c.Expression.(clause.From)
	if !ok {
		return
	}
	var rewritten []clause.Join
	for i, j := range from.Joins {
		if len(j.ON.Exprs) != 1 {
			continue
		}
		if k, ours := j.ON.Exprs[0].(keyedOn); ours {
			if rewritten == nil {
				rewritten = slices.Clone(from.Joins)
			}
			rewritten[i].ON = clause.Where{Exprs: k.written}
		}
	}
	if rewritten == nil {
		return
	}
	from.Joins = rewritten
	c.Expression = from
	st.Clauses["FROM"] = c
}
