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

// This file holds what the filter makes of the statements GORM's association
// mode issues: the join table a many-to-many association is written through,
// the keys GORM writes into a statement's conditions, and the join by table
// name that reads an association.

// tenantFieldOf returns the field GORM binds a statement's model's tenant
// column to, nil when the model is not scoped, or the refusal of a model no one
// tenant column can be taken from.
//
// A model is scoped by embedding the convention (conventionFieldOf). The table
// GORM writes a many-to-many association through is scoped by the models it
// joins as well (joinTenantField), whether GORM built its model or
// SetupJoinTable named one, and whether or not that model embeds the
// convention: a link row names one row of each side, and a scoped side puts its
// tenant into the link. A scoped model declaring an association whose read the
// filter cannot hold to a tenant is refused as a model no tenant column can be
// taken from is (unjudgedRead).
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

// unjudgedRead is the refusal of a scoped model declaring a many-to-many
// association with a global model whose read the filter cannot hold to a
// tenant, given the field its tenant column is bound to; nil when it declares
// none.
//
// GORM's association mode reads a many-to-many association as a read of the
// other side, with the join table joined in by its table name, and nothing of
// the owner reaches that read — not its model, its schema, nor the association:
// only the ON does, with the owner's key GORM writes there. Where the other
// side is scoped, the read is held to the tenant by its own model, and the ON
// ties the join table to that model's key (fromJoins). Where it is global, the
// tenant half of the owner's key in the ON is all that says which tenant the
// read is of, and the filter recognises it by its type (onReading). Two
// declarations leave it out:
//
//   - the owner joined by the rest of its key, so that the join table carries
//     no tenant and the ON binds none;
//   - the owner's tenant column bound to a field of its own that is not of the
//     identifier type, whose value in the ON is a plain value like any other.
//
// The read of such an association cannot be judged, and reads the links of
// every tenant. The association is the owner's declaration, so the owner is
// what is refused: on every statement through it, under any context, as a
// model embedding the convention where GORM does not read it is. The read on
// an owner the caller builds by its key goes through no statement of the
// owner's, and stays out of reach; the design writes it as a non-guarantee.
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

// identifierField reports whether a field is of the identifier type, directly
// or through pointers.
func identifierField(f *gormschema.Field) bool {
	t := f.FieldType
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == identifierType
}

// joinedBy lists the many-to-many associations a schema is the join table of,
// ordered by the model declaring each and its field.
//
// GORM gives a join table's schema a relationship to the model on each side —
// on the model it builds, and copied onto the one SetupJoinTable names — and
// the model declaring the association carries it, with that schema as its join
// table. The answer depends on the declarations alone.
//
// A schema's relationships are read under the lock GORM writes them under:
// parsing a model with a has-one or has-many association writes a back
// reference into the associated model's relationships, and that model may be
// the one a statement runs through.
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

// joinTenantField returns the field a join table's tenant column is bound to,
// nil when no model it joins is scoped and it does not embed the convention
// itself, or the refusal of a join table no one tenant column can be taken
// from.
//
// The tenant column is found by where it comes from, as a model's is: it is
// the column of the join table that the tenant half of a scoped side's key is
// written to. GORM builds a link row from the keys of the two rows it links, so
// a scoped side joined by a key that includes its tenant column puts its tenant
// into the link, and a link then belongs to that tenant. A scoped side joined
// by the rest of its key puts nothing there; a statement reading or writing its
// rows is scoped by its own model, and a link names its row within the tenant
// the link belongs to. The tenant column is that column whether or not the join
// model embeds the convention: the read GORM's association mode joins the table
// into names it by its table name, and sees the keys it binds, not the model.
// A join table between models none of which is scoped is judged by its own
// model, as any model is.
//
// Three declarations leave no one tenant column to judge, and a join table
// declared any of these ways is refused on every statement through it, under
// any context, rather than read as a global table:
//
//   - a scoped side and no tenant column: every scoped side joined by the rest
//     of its key, so that a link belongs to no tenant and names a row of every
//     tenant holding the key;
//   - two sides putting their tenants on two columns — the join GORM builds
//     between two scoped models when no joinForeignKey and joinReferences name
//     one column for both — since a link belongs to one tenant and nothing
//     holds the two equal;
//   - a join model embedding the convention on a column other than the one the
//     sides put their tenant on, which is the same two columns.
//
// A join model with a refused declaration of its own, or a side that has one,
// is refused as that model is.
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

// linkColumn is the column of an association's join table the tenant half of
// one side's key is written to, or empty when the association joins that side
// by a key without it.
func linkColumn(m *gormschema.Relationship, own bool, tenantField *gormschema.Field) string {
	for _, ref := range m.References {
		if ref.OwnPrimaryKey == own && ref.PrimaryValue == "" && ref.PrimaryKey != nil && ref.ForeignKey != nil &&
			ref.PrimaryKey.DBName == tenantField.DBName {
			return ref.ForeignKey.DBName
		}
	}
	return ""
}

// keyHalves judges the conditions a statement carries on its tenant column,
// fills the tenant half of the keys GORM's association mode wrote into them,
// and collects the tenants other than the context's the conditions name.
//
// GORM's association mode addresses records by their whole key, and writes the
// key into the statement's conditions as it reads it off the owner and the
// records given, before any callback runs: always an IN over a tuple of the
// key's columns, each qualified by the name of the table the key addresses, one
// tuple per record. A record the caller addressed by the rest of its key
// leaves the tenant half of its tuple empty, as a write addressed by a struct's
// key does, and filling it from the context is that fill carried over to the
// key GORM has already written out. So a half is filled only in such a tuple:
// over columns that are, named as GORM names them, a whole key of the table
// (key), holding the tenant half empty beside another part of the key written.
// A tuple whose other parts are all empty addresses nothing, and filling its
// tenant half would turn it into one addressing the whole tenant, as it would
// a struct's key.
//
// Every other condition on the tenant column is the caller's — a map, a
// struct, an equality, an IN over the column alone, a tuple over columns that
// are no key or are named otherwise — and is judged as written, never filled:
// an empty value stays empty, so the statement reaches what the condition
// selects, nothing, or is refused where the empty value is an identifier, which
// refuses to be bound. Filling it would turn a write whose value came out empty
// into a write over the whole tenant. A tuple the caller writes by hand exactly
// as GORM writes a key is taken for a key: the filter reads the shape of a
// condition, not who wrote it, and the fill addresses only what that key names
// in the context's tenant.
//
// A half is found by the column it is bound to (at), and every value on the
// tenant column is judged, filled or not: one naming another tenant is noted.
//
// The conditions are copied where a half is filled, never written into: the
// expressions are the caller's, or GORM's built for this one call, and the same
// statement run again under another context has to find them as they were.
type keyHalves struct {
	at func(clause.Column) bool
	// key reports whether a tuple of columns is a whole key of the table the
	// conditions address.
	key   func([]clause.Column) bool
	id    string
	named []string
}

// exprs is the expression list with every tenant half in it filled.
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

// expr is one expression with every tenant half of a key in it filled. The
// expressions a condition on a column is written in, and the groups they nest
// in — a NOT among them, which GORM's association mode writes the records it
// keeps in — are looked into; anything else is left as it stands.
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

// in judges the values an IN lists on the tenant column — a value of its own
// under one column, or a tuple under several — and fills the tenant half of
// each tuple that is a key.
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

// judge notes a tenant half naming a tenant other than the context's.
func (k *keyHalves) judge(v any) {
	if s, ok := stringValue(reflect.ValueOf(v)); ok && s != "" && s != k.id {
		k.named = append(k.named, s)
	}
}

// filled is one tenant half of a key: the context's identifier in place of an
// empty one, of the same type, and anything else as it stands.
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

// otherPartWritten reports whether a key's tuple holds a value in a part other
// than its tenant halves.
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

// ownColumn reports whether a column is the statement's own tenant column,
// named the way GORM names a column of the statement's table in the conditions
// it writes (ownTable).
func ownColumn(st *gorm.Statement, field *gormschema.Field) func(clause.Column) bool {
	return func(c clause.Column) bool {
		return ownTable(st, c) && c.Name == field.DBName
	}
}

// ownTable reports whether a column is named as a column of the statement's own
// table: unqualified, or qualified by the statement's table or the
// current-table placeholder. A column qualified by another table is another
// table's, whatever its name.
func ownTable(st *gorm.Statement, c clause.Column) bool {
	return !c.Raw && (c.Table == "" || c.Table == clause.CurrentTable || c.Table == st.Table)
}

// wholeKey reports whether a tuple of columns is a whole key of the
// statement's model, as GORM's association mode writes one: every column
// qualified by the name of the statement's table, and the columns together the
// model's primary key or the columns one side of an association binds its key
// to on the model's table (keyColumns).
//
// GORM qualifies every column of a key it writes by the name of the table the
// key addresses. A condition of the caller's names its columns as it likes —
// bare, or by the current-table placeholder a map or a struct is written with —
// and a tuple over the whole key written that way is the caller's, and left as
// written.
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

// keyColumns lists the sets of columns a key of a model is written over on its
// table, each sorted: its primary key, and for each association involving the
// model the columns one side's key is bound to there — the foreign key of a
// has-one or has-many reaching the model, of a belongs-to it declares, and of
// each side of a many-to-many association the model is the join table of.
//
// These are the columns GORM's association mode writes a record's key over when
// the model is the one its statement addresses. The associations are the ones
// the model's schema carries — its own, and the back reference GORM writes into
// it for a has-one or has-many reaching it — and the ones it is the join table
// of (joinedBy); a key over columns none of them names is not one of these.
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

// identifierType is the type of the convention's field, and of the fields GORM
// copies it into on a join model it builds.
var identifierType = reflect.TypeOf(tenant.ID(""))

// isIdentifier reports whether a value is of the identifier type, directly or
// through pointers.
func isIdentifier(v any) bool {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == identifierType
}

// fromJoin is a join of a read's FROM clause into a table that carries
// tenants, with the column it carries them on.
//
// GORM's association mode reads an association through a join table by
// joining that table into a read of the associated model in the FROM clause,
// named by its table name: its ON ties the join table to the associated
// model's key, and carries the owner's key as an IN over the join table's
// columns. The table's model appears nowhere in the statement, so what the
// join reaches is read off the ON as GORM writes it (fromJoins).
type fromJoin struct {
	index   int
	table   string
	column  string
	written []clause.Expression
}

// fromJoins lists the joins of a read's FROM clause into a table that carries
// tenants, or the refusal of a read joining one no one tenant column can be
// taken from, given the field the read's own model binds its tenant column to.
//
// The joined table carries tenants on a column where the ON binds the tenant
// half of a key — a value of the identifier type, which the convention's field
// and the fields GORM copies it into carry — or ties it to the read's own
// tenant column. The same two declarations are refused here as on a join
// table's own statements (joinTenantField), as far as the ON shows them: a
// scoped model tied by the rest of its key to a joined table the ON shows no
// tenant column of, and a joined table carrying tenants on two columns. A join
// whose ON shows neither carries no tenant the filter can see.
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

// onReading is what the ON of one join shows about the table it joins.
type onReading struct {
	st    *gorm.Statement
	field *gormschema.Field
	table string
	// columns are the joined table's columns found to carry tenants.
	columns []string
	// byKey reports an equality tying the joined table to a key column of the
	// read's own model other than its tenant column.
	byKey bool
}

// read goes through an expression list as keyHalves does.
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

// readIN notes the joined table's columns an IN binds a tenant half to.
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

// readEq notes an equality binding a tenant half to a column of the joined
// table, or tying one to a key column of the read's own model.
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

// carries notes a column of the joined table as one it carries tenants on.
func (o *onReading) carries(col clause.Column) {
	if !col.Raw && col.Table == o.table && !slices.Contains(o.columns, col.Name) {
		o.columns = append(o.columns, col.Name)
	}
}

// keyedOn stands in the ON of a join into a table carrying tenants, in place
// of the conditions written there: it renders them with the tenant half of a
// key filled from the context and the joined table held to the context's
// tenant, and keeps them as written for the statement's next execution.
type keyedOn struct {
	written  []clause.Expression
	rendered []clause.Expression
}

// Build renders the conditions as the ON of the join renders its own.
func (k keyedOn) Build(builder clause.Builder) {
	clause.Where{Exprs: slices.Clone(k.rendered)}.Build(builder)
}

// writtenOn is the ON conditions of a join as written, with this plugin's
// stand-in taken back out.
func writtenOn(on clause.Where) []clause.Expression {
	if len(on.Exprs) == 1 {
		if k, ours := on.Exprs[0].(keyedOn); ours {
			return k.written
		}
	}
	return on.Exprs
}

// applyFromJoins puts the tenant into each join of a read's FROM clause into a
// table carrying tenants: the tenant half of the keys its ON binds, and an
// equality holding the joined table to the context's tenant on the column it
// carries tenants on, so that a key naming another tenant reaches nothing.
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

// releaseFromJoins takes this plugin's stand-in back out of every join of a
// read's FROM clause, leaving the ON conditions as they were written.
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
