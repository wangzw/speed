package catalog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormschema "gorm.io/gorm/schema"
	"gorm.io/gorm/utils"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

const callbackName = "tenant:scope"

const conventionField = "TenantID"

var conventionType = reflect.TypeOf(tenant.Model{})

const whereKey = "WHERE"

// filter scopes statements to the tenant their context carries. It holds no
// state: one instance is shared by every handle and every registry.
type filter struct{}

func newFilter() *filter { return &filter{} }

// Name returns the plugin's name in GORM's plugin registry.
func (f *filter) Name() string { return "github.com/vislake/speed/pkg/tenant/catalog" }

// Initialize registers the scoping callbacks on handle without keeping it. Row,
// Rows and Scan run the row processor, so the filter must be registered there as
// well as on the query processor.
func (f *filter) Initialize(handle *gorm.DB) error {
	registered := []func(*gorm.DB) error{
		func(h *gorm.DB) error {
			return h.Callback().Query().Before("gorm:query").Register(callbackName, f.scopeRead)
		},
		func(h *gorm.DB) error {
			return h.Callback().Row().Before("gorm:row").Register(callbackName, f.scopeRow)
		},
		func(h *gorm.DB) error {
			return h.Callback().Update().Before("gorm:update").Register(callbackName, f.scopeUpdate)
		},
		func(h *gorm.DB) error {
			return h.Callback().Delete().Before("gorm:delete").Register(callbackName, f.scopeDelete)
		},
		func(h *gorm.DB) error {
			return h.Callback().Create().Before("gorm:create").Register(callbackName, f.fillColumn)
		},
	}
	for _, register := range registered {
		if err := register(handle); err != nil {
			return err
		}
	}
	return nil
}

type scope int

const (
	unscoped scope = iota
	crossing
	refused
	tenanted
)

type reached struct {
	joins []scopedJoin
	from  []fromJoin
}

func (r reached) any() bool { return len(r.joins) > 0 || len(r.from) > 0 }

func (r reached) release(st *gorm.Statement) {
	releaseJoins(st, r.joins)
	releaseFromJoins(st)
}

// scopeOf judges one statement. The order of its checks must not change: model
// first, then the cross-tenant mark, then the identifier.
func scopeOf(st *gorm.Statement, field *gormschema.Field, unjudged error, r reached) (string, scope, error) {
	if unjudged != nil {
		releaseCondition(st)
		r.release(st)
		return "", refused, unjudged
	}
	if field == nil && !r.any() {
		return "", unscoped, nil
	}
	if tenantctx.CrossTenant(st.Context) {
		releaseCondition(st)
		r.release(st)
		return "", crossing, nil
	}
	id, ok := tenantctx.ID(st.Context)
	if !ok {
		releaseCondition(st)
		r.release(st)
		return "", refused, noTenant(st, field, r)
	}
	return id, tenanted, nil
}

func (f *filter) scopeRead(tx *gorm.DB) { f.read(tx, false) }

func (f *filter) scopeRow(tx *gorm.DB) { f.read(tx, handsBackOneRow(tx)) }

// handsBackOneRow reports whether a row-processor statement is a Row; GORM's row
// step tells Row from Rows and Scan by the "rows" setting.
func handsBackOneRow(tx *gorm.DB) bool {
	setting, ok := tx.Get("rows")
	many, isBool := setting.(bool)
	return ok && isBool && !many
}

func (f *filter) read(tx *gorm.DB, oneRow bool) {
	st := tx.Statement
	field, unjudged := tenantFieldOf(st.Schema)
	var r reached
	if unjudged == nil {
		r.joins, unjudged = scopedJoins(st)
	}
	if unjudged == nil {
		r.from, unjudged = fromJoins(st, field)
	}
	id, s, refusal := scopeOf(st, field, unjudged, r)
	switch s {
	case tenanted:
	case refused:
		refuseRead(tx, refusal, oneRow)
		return
	default:
		return
	}
	if field != nil {
		applyCondition(st, field, id, false)
	}
	applyJoins(st, r.joins, id)
	applyFromJoins(st, r.from, id)
}

func refuseRead(tx *gorm.DB, refusal error, oneRow bool) {
	st := tx.Statement
	switch {
	case oneRow && tx.Error == nil && !tx.DryRun:
		_ = tx.AddError(refusal)
		st.Dest = refusedRow(refusal)
	case tx.DryRun && st.SQL.Len() == 0:
		_ = tx.AddError(refusal)
		renderRefusal(st, tx.Error)
	default:
		_ = tx.AddError(refusal)
	}
}

func renderRefusal(st *gorm.Statement, err error) {
	st.SQL.WriteString("SELECT ")
	st.AddVar(&st.SQL, refusal{err: err})
}

type refusal struct{ err error }

// Value fails with the error the refusal carries.
func (r refusal) Value() (driver.Value, error) { return nil, r.err }

func refusedRow(err error) *sql.Row {
	pool := sql.OpenDB(refusingConnector{err: err})
	defer func() { _ = pool.Close() }()
	return pool.QueryRowContext(context.Background(), "")
}

type refusingConnector struct{ err error }

// Connect refuses the connection with the connector's error.
func (c refusingConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }

// Driver returns a driver that opens nothing.
func (c refusingConnector) Driver() driver.Driver { return refusingDriver(c) }

type refusingDriver struct{ err error }

// Open refuses the connection with the driver's error.
func (d refusingDriver) Open(string) (driver.Conn, error) { return nil, d.err }

// scopedJoin is one association join that reaches a scoped model. A level whose
// alias is not recognised renders the tenant condition, so an unknown table fails
// the statement instead of reading across tenants.
type scopedJoin struct {
	index    int
	model    string
	columns  map[string]string
	unscoped map[string]bool
	fallback string
}

func scopedJoins(st *gorm.Statement) ([]scopedJoin, error) {
	if st.Schema == nil || len(st.Joins) == 0 {
		return nil, nil
	}
	var out []scopedJoin
	for i, j := range st.Joins {
		if j.Expression != nil {
			continue
		}
		chain := relationChain(st.Schema, j.Name)
		if len(chain) == 0 {
			continue
		}
		sj := scopedJoin{index: i, columns: map[string]string{}, unscoped: map[string]bool{}}
		parent := ""
		for level, rel := range chain {
			current := rel.Name
			if parent != "" {
				current = utils.NestedRelationName(parent, rel.Name)
			}
			alias := current
			if level == len(chain)-1 && j.Alias != "" {
				alias = j.Alias
			}
			f, unjudged := tenantFieldOf(rel.FieldSchema)
			switch {
			case unjudged != nil:
				return nil, unjudged
			case f != nil:
				sj.columns[alias] = f.DBName
				sj.fallback = f.DBName
				if sj.model == "" {
					sj.model = rel.FieldSchema.Name
				}
			default:
				sj.unscoped[alias] = true
			}
			parent = current
		}
		if sj.model != "" {
			out = append(out, sj)
		}
	}
	return out, nil
}

func relationChain(s *gormschema.Schema, name string) []*gormschema.Relationship {
	if rel, ok := s.Relationships.Relations[name]; ok {
		return []*gormschema.Relationship{rel}
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return nil
	}
	chain := make([]*gormschema.Relationship, 0, len(parts))
	relations := s.Relationships.Relations
	for _, part := range parts {
		rel, ok := relations[part]
		if !ok {
			return nil
		}
		chain = append(chain, rel)
		relations = rel.FieldSchema.Relationships.Relations
	}
	return chain
}

type joinTerm struct {
	id       string
	columns  map[string]string
	unscoped map[string]bool
	fallback string
}

// Build renders the tenant condition for the join level whose ON clause is being
// built.
func (t joinTerm) Build(builder clause.Builder) {
	alias := ""
	if st, ok := builder.(*gorm.Statement); ok {
		alias = st.Table
	}
	if t.unscoped[alias] {
		clause.Expr{SQL: "1 = 1"}.Build(builder)
		return
	}
	column, ok := t.columns[alias]
	if !ok {
		column = t.fallback
	}
	clause.Eq{
		Column: clause.Column{Table: clause.CurrentTable, Name: column},
		Value:  t.id,
	}.Build(builder)
}

func applyJoins(st *gorm.Statement, joins []scopedJoin, id string) {
	for _, sj := range joins {
		j := &st.Joins[sj.index]
		exprs := withoutJoinTerm(j.On)
		exprs = append(exprs, joinTerm{id: id, columns: sj.columns, unscoped: sj.unscoped, fallback: sj.fallback})
		j.On = &clause.Where{Exprs: exprs}
	}
}

func releaseJoins(st *gorm.Statement, joins []scopedJoin) {
	for _, sj := range joins {
		j := &st.Joins[sj.index]
		if j.On == nil {
			continue
		}
		if exprs := withoutJoinTerm(j.On); len(exprs) > 0 {
			j.On = &clause.Where{Exprs: exprs}
		} else {
			j.On = nil
		}
	}
}

func withoutJoinTerm(on *clause.Where) []clause.Expression {
	if on == nil {
		return nil
	}
	out := make([]clause.Expression, 0, len(on.Exprs)+1)
	for _, e := range on.Exprs {
		if _, ours := e.(joinTerm); !ours {
			out = append(out, e)
		}
	}
	return out
}

func (f *filter) scopeDelete(tx *gorm.DB) {
	st := tx.Statement
	field, unjudged := tenantFieldOf(st.Schema)
	id, s, refusal := scopeOf(st, field, unjudged, reached{})
	switch s {
	case tenanted:
	case refused:
		_ = tx.AddError(refusal)
		return
	default:
		return
	}
	makeAddressable(st)
	keyed := []reflect.Value{st.ReflectValue}
	if st.Model != nil && !samePointer(st.Model, st.Dest) {
		keyed = append(keyed, reflect.ValueOf(st.Model))
	}
	if err := fillKey(st, field, id, keyed...); err != nil {
		_ = tx.AddError(err)
		return
	}
	if named := applyCondition(st, field, id, !tx.AllowGlobalUpdate); len(named) > 0 {
		_ = tx.AddError(keyNaming(st, named[0]))
	}
}

func (f *filter) scopeUpdate(tx *gorm.DB) {
	st := tx.Statement
	field, unjudged := tenantFieldOf(st.Schema)
	id, s, refusal := scopeOf(st, field, unjudged, reached{})
	var judge func(any) error
	switch s {
	case crossing:
		judge = someTenant(st)
	case tenanted:
		judge = ownTenant(st, id)
	case refused:
		_ = tx.AddError(refusal)
		return
	default:
		return
	}
	if err := judgeEach(assignedValues(st, field), judge); err != nil {
		_ = tx.AddError(err)
		return
	}
	if s == crossing {
		return
	}
	if err := fillKey(st, field, id, st.ReflectValue); err != nil {
		_ = tx.AddError(err)
		return
	}
	if named := applyCondition(st, field, id, !tx.AllowGlobalUpdate); len(named) > 0 {
		_ = tx.AddError(keyNaming(st, named[0]))
	}
}

func keyNaming(st *gorm.Statement, named string) error {
	return fmt.Errorf("%w: %s is addressed by a key naming tenant %q", tenant.ErrExplicitTenantMismatch,
		st.Schema.Name, named)
}

func (f *filter) fillColumn(tx *gorm.DB) {
	st := tx.Statement
	field, unjudged := tenantFieldOf(st.Schema)
	id, s, refusal := scopeOf(st, field, unjudged, reached{})
	var judge func(any) error
	switch s {
	case tenanted:
		judge = ownTenant(st, id)
	case crossing:
		judge = someTenant(st)
	case refused:
		_ = tx.AddError(refusal)
		return
	default:
		return
	}
	if err := writeRows(st, field, id, s == tenanted, judge); err != nil {
		_ = tx.AddError(err)
		return
	}
	if err := judgeEach(upsertValues(st, field), judge); err != nil {
		_ = tx.AddError(err)
	}
}

// scopeTerm is the tenant equality put into a WHERE. It is its own type so that
// a re-executed statement can find and replace it, and so that GORM's
// missing-WHERE check can set it aside.
type scopeTerm struct {
	column           string
	id               string
	written          []clause.Expression
	requireCondition bool
}

// Build renders the equality, and fails with gorm.ErrMissingWhereClause when
// requireCondition is set and no other condition narrows the write.
func (t scopeTerm) Build(builder clause.Builder) {
	if t.requireCondition && !narrowed(builder) {
		_ = builder.AddError(gorm.ErrMissingWhereClause)
	}
	clause.Eq{
		Column: clause.Column{Table: clause.CurrentTable, Name: t.column},
		Value:  t.id,
	}.Build(builder)
}

// softDeleteKey is the statement setting GORM leaves when the model soft deletes.
const softDeleteKey = "soft_delete_enabled"

func narrowed(builder clause.Builder) bool {
	st, ok := builder.(*gorm.Statement)
	if !ok {
		return true
	}
	where, _ := st.Clauses[whereKey].Expression.(clause.Where)
	others := 0
	for _, e := range where.Exprs {
		if _, ours := e.(scopeTerm); !ours {
			others++
		}
	}
	if _, soft := st.Clauses[softDeleteKey]; soft {
		return others > 1
	}
	return others > 0
}

// applyCondition groups the caller's conditions before adding the equality:
// appending to an OR list would leave the OR branch unscoped.
func applyCondition(st *gorm.Statement, field *gormschema.Field, id string, requireCondition bool) []string {
	written := writtenConditions(st)
	halves := keyHalves{at: ownColumn(st, field), key: wholeKey(st), id: id}
	exprs := make([]clause.Expression, 0, 2)
	if group := grouped(halves.exprs(written)); group != nil {
		exprs = append(exprs, group)
	}
	exprs = append(exprs, scopeTerm{
		column: field.DBName, id: id, written: written, requireCondition: requireCondition,
	})
	if st.Clauses == nil {
		st.Clauses = map[string]clause.Clause{}
	}
	c := st.Clauses[whereKey]
	c.Name = whereKey
	c.Expression = clause.Where{Exprs: exprs}
	st.Clauses[whereKey] = c
	return halves.named
}

func writtenConditions(st *gorm.Statement) []clause.Expression {
	where, ok := st.Clauses[whereKey].Expression.(clause.Where)
	if !ok {
		return nil
	}
	for i, e := range where.Exprs {
		if term, ours := e.(scopeTerm); ours {
			restored := make([]clause.Expression, 0, len(term.written)+len(where.Exprs)-i-1)
			restored = append(restored, term.written...)
			return append(restored, where.Exprs[i+1:]...)
		}
	}
	return append([]clause.Expression(nil), where.Exprs...)
}

func releaseCondition(st *gorm.Statement) {
	where, ok := st.Clauses[whereKey].Expression.(clause.Where)
	if !ok || !carriesTerm(where) {
		return
	}
	written := writtenConditions(st)
	if len(written) == 0 {
		delete(st.Clauses, whereKey)
		return
	}
	c := st.Clauses[whereKey]
	c.Expression = clause.Where{Exprs: written}
	st.Clauses[whereKey] = c
}

func carriesTerm(where clause.Where) bool {
	for _, e := range where.Exprs {
		if _, ours := e.(scopeTerm); ours {
			return true
		}
	}
	return false
}

// grouped keeps the group an AND group where it can: FirstOrCreate and
// FirstOrInit read equalities back only from AND groups.
func grouped(written []clause.Expression) clause.Expression {
	exprs := append([]clause.Expression(nil), written...)
	if len(exprs) == 1 {
		if and, ok := exprs[0].(clause.AndConditions); ok {
			exprs = append([]clause.Expression(nil), and.Exprs...)
		}
	}
	for i, e := range exprs {
		if or, ok := e.(clause.OrConditions); !ok || len(or.Exprs) > 1 {
			if i != 0 {
				exprs[0], exprs[i] = exprs[i], exprs[0]
			}
			break
		}
	}
	switch len(exprs) {
	case 0:
		return nil
	case 1:
		if eq, ok := exprs[0].(clause.Eq); ok && !rawColumn(eq.Column) {
			return clause.AndConditions{Exprs: exprs}
		}
		return parenthesized{expr: exprs[0]}
	default:
		return clause.AndConditions{Exprs: exprs}
	}
}

type parenthesized struct{ expr clause.Expression }

// Build renders the expression between parentheses.
func (p parenthesized) Build(builder clause.Builder) {
	_ = builder.WriteByte('(')
	p.expr.Build(builder)
	_ = builder.WriteByte(')')
}

func rawColumn(column interface{}) bool {
	c, ok := column.(clause.Column)
	return ok && c.Raw
}

// fillKey fills the tenant half of the key only when another key part is
// written: an all-zero key addresses nothing, and filling it would address the
// whole tenant.
func fillKey(st *gorm.Statement, field *gormschema.Field, id string, values ...reflect.Value) error {
	if !inKey(st.Schema, field) {
		return nil
	}
	for _, rv := range values {
		for _, v := range structValues(rv, st.Schema.ModelType) {
			written, isZero := field.ValueOf(st.Context, v)
			if !isZero {
				s, ok := stringValue(reflect.ValueOf(written))
				switch {
				case !ok:
					return fmt.Errorf("%w: %s is addressed by a key whose tenant half is %s, which "+
						"cannot be read as the context's tenant", tenant.ErrExplicitTenantMismatch,
						st.Schema.Name, describe(written))
				case s != id:
					return fmt.Errorf("%w: %s is addressed by a key naming tenant %q",
						tenant.ErrExplicitTenantMismatch, st.Schema.Name, s)
				}
				continue
			}
			if !v.CanAddr() || !otherKeyWritten(st, field, v) {
				continue
			}
			if err := field.Set(st.Context, v, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func inKey(s *gormschema.Schema, field *gormschema.Field) bool {
	for _, key := range s.PrimaryFields {
		if key == field {
			return true
		}
	}
	return false
}

func otherKeyWritten(st *gorm.Statement, field *gormschema.Field, v reflect.Value) bool {
	for _, key := range st.Schema.PrimaryFields {
		if key == field {
			continue
		}
		if _, isZero := key.ValueOf(st.Context, v); !isZero {
			return true
		}
	}
	return false
}

func structValues(rv reflect.Value, model reflect.Type) []reflect.Value {
	rv = indirect(rv)
	switch rv.Kind() {
	case reflect.Struct:
		if rv.Type() == model {
			return []reflect.Value{rv}
		}
	case reflect.Slice, reflect.Array:
		out := make([]reflect.Value, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			if elem := indirect(rv.Index(i)); elem.Kind() == reflect.Struct && elem.Type() == model {
				out = append(out, elem)
			}
		}
		return out
	}
	return nil
}

func indirect(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func makeAddressable(st *gorm.Statement) {
	rv := st.ReflectValue
	if !rv.IsValid() || rv.CanAddr() || (rv.Kind() != reflect.Struct && rv.Kind() != reflect.Array) {
		return
	}
	copied := reflect.New(rv.Type())
	copied.Elem().Set(rv)
	if st.Model != nil && reflect.TypeOf(st.Model) == rv.Type() {
		st.Model = copied.Interface()
	}
	st.Dest = copied.Interface()
	st.ReflectValue = copied.Elem()
}

func samePointer(a, b any) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	return va.Kind() == reflect.Pointer && vb.Kind() == reflect.Pointer &&
		va.Type() == vb.Type() && va.Pointer() == vb.Pointer()
}

func ownTenant(st *gorm.Statement, id string) func(any) error {
	return func(v any) error {
		if u, ok := v.(unreadTarget); ok {
			return u.refusal(st)
		}
		if s, ok := stringValue(reflect.ValueOf(v)); ok {
			if s == id {
				return nil
			}
			return fmt.Errorf("%w: %s names tenant %q in its tenant column",
				tenant.ErrExplicitTenantMismatch, st.Schema.Name, s)
		}
		return fmt.Errorf("%w: %s puts %s in its tenant column, which cannot be read as "+
			"the context's tenant", tenant.ErrExplicitTenantMismatch, st.Schema.Name, describe(v))
	}
}

func someTenant(st *gorm.Statement) func(any) error {
	return func(v any) error {
		if u, ok := v.(unreadTarget); ok {
			return u.refusal(st)
		}
		if isNull(v) {
			return fmt.Errorf("%w: %s puts a null in its tenant column across tenants",
				tenant.ErrNoTenantInContext, st.Schema.Name)
		}
		s, ok := stringValue(reflect.ValueOf(v))
		switch {
		case !ok:
			return nil
		case s == "":
			return fmt.Errorf("%w: %s is written across tenants with an empty tenant column",
				tenant.ErrNoTenantInContext, st.Schema.Name)
		case !tenant.IsIdentifier(s):
			return fmt.Errorf("%w: %s is written across tenants with a tenant column carrying "+
				"a blank or a control character", tenant.ErrInvalidTenant, st.Schema.Name)
		}
		return nil
	}
}

func judgeEach(values []any, judge func(any) error) error {
	for _, v := range values {
		if err := judge(v); err != nil {
			return err
		}
	}
	return nil
}

func writeRows(st *gorm.Statement, field *gormschema.Field, id string, fill bool, judge func(any) error) error {
	selected, restricted := st.SelectAndOmitColumns(true, false)
	if maps, ok := insertedMaps(st.Dest); ok {
		for _, m := range maps {
			if err := writeMap(st, field, m, id, fill, judge, selected, restricted); err != nil {
				return err
			}
		}
		return nil
	}
	if !insertSelects(field, selected, restricted) {
		return leftOut(st)
	}
	if fill {
		makeAddressable(st)
	}
	spellings := otherSpellings(st, field)
	for _, v := range structValues(st.ReflectValue, st.Schema.ModelType) {
		if err := judgeSpellings(st, spellings, v, judge, selected, restricted); err != nil {
			return err
		}
		written, isZero := field.ValueOf(st.Context, v)
		if !isZero {
			if err := judge(written); err != nil {
				return err
			}
			continue
		}
		if !fill {
			return unnamedAcross(st)
		}
		if !v.CanAddr() {
			// Nothing can be written here; the identifier refuses to be bound empty, so the
			// insert still fails.
			continue
		}
		if err := field.Set(st.Context, v, id); err != nil {
			return err
		}
	}
	return nil
}

// otherSpellings lists fields that write to a column the database may read as
// the tenant column. SQLite keeps the first value an insert gives one column, so
// the spelling declared first decides the row's tenant.
func otherSpellings(st *gorm.Statement, field *gormschema.Field) []*gormschema.Field {
	var out []*gormschema.Field
	for _, f := range st.Schema.Fields {
		if f != field && f.DBName != "" && insertWrites(st.Schema, f) &&
			targetReaches(st, clause.Column{Name: f.DBName}, field) == tenantColumn {
			out = append(out, f)
		}
	}
	return out
}

func insertWrites(s *gormschema.Schema, f *gormschema.Field) bool {
	if s.FieldsByDBName[f.DBName] == f {
		return true
	}
	for _, d := range s.FieldsWithDefaultDBValue {
		if d == f {
			return true
		}
	}
	return false
}

func judgeSpellings(st *gorm.Statement, spellings []*gormschema.Field, v reflect.Value, judge func(any) error,
	selected map[string]bool, restricted bool,
) error {
	for _, f := range spellings {
		if !insertSelects(f, selected, restricted) {
			continue
		}
		value, isZero := f.ValueOf(st.Context, v)
		switch {
		case !isZero:
		case f.DefaultValueInterface != nil:
			value = f.DefaultValueInterface
		case f.HasDefaultValue:
			continue
		case f.AutoCreateTime > 0 || f.AutoUpdateTime > 0:
			value = st.NowFunc()
		}
		if err := judge(value); err != nil {
			return err
		}
	}
	return nil
}

func insertSelects(f *gormschema.Field, selected map[string]bool, restricted bool) bool {
	sel, explicit := selected[f.DBName]
	stamped := f.AutoCreateTime > 0 || f.AutoUpdateTime > 0
	return (explicit && sel) || (!explicit && (!restricted || stamped))
}

func leftOut(st *gorm.Statement) error {
	return fmt.Errorf("%w: %s is inserted with its tenant column left out of the row — by the "+
		"statement's selection or omission, or because the field bound to it is not written on "+
		"insert — so the row would belong to no tenant", tenant.ErrNoTenantInContext, st.Schema.Name)
}

func writeMap(st *gorm.Statement, field *gormschema.Field, m map[string]interface{},
	id string, fill bool, judge func(any) error, selected map[string]bool, restricted bool,
) error {
	if m == nil {
		return nil
	}
	written := false
	for key, v := range m {
		if !keyReaches(st, key, field) || !assigned(selected, restricted, keyColumn(st, key)) {
			continue
		}
		written = true
		if err := judge(v); err != nil {
			return err
		}
	}
	switch {
	case written:
		return nil
	case !assigned(selected, restricted, field.DBName):
		return leftOut(st)
	case !fill:
		return unnamedAcross(st)
	}
	m[field.DBName] = id
	return nil
}

func unnamedAcross(st *gorm.Statement) error {
	return fmt.Errorf("%w: %s is inserted across tenants with no tenant in its tenant column",
		tenant.ErrNoTenantInContext, st.Schema.Name)
}

func insertedMaps(dest any) ([]map[string]interface{}, bool) {
	switch v := dest.(type) {
	case map[string]interface{}:
		return []map[string]interface{}{v}, true
	case *map[string]interface{}:
		if v == nil {
			return nil, true
		}
		return []map[string]interface{}{*v}, true
	case []map[string]interface{}:
		return v, true
	case *[]map[string]interface{}:
		if v == nil {
			return nil, true
		}
		return *v, true
	}
	return nil, false
}

func assignedValues(st *gorm.Statement, field *gormschema.Field) []any {
	var out []any
	if c, ok := st.Clauses["SET"]; ok {
		set, _ := c.Expression.(clause.Set)
		for _, a := range set {
			switch targetReaches(st, a.Column, field) {
			case tenantColumn:
				out = append(out, a.Value)
			case unread:
				out = append(out, unreadTarget{name: a.Column.Name})
			}
		}
	}
	return append(out, destinationValues(st, field)...)
}

func destinationValues(st *gorm.Statement, field *gormschema.Field) []any {
	updating := indirect(reflect.ValueOf(st.Dest))
	if !updating.IsValid() {
		return nil
	}
	selected, restricted := st.SelectAndOmitColumns(false, true)
	if m, ok := updating.Interface().(map[string]interface{}); ok {
		var out []any
		for key, v := range m {
			if keyReaches(st, key, field) && assigned(selected, restricted, keyColumn(st, key)) {
				out = append(out, v)
			}
		}
		if st.SkipHooks {
			return out
		}
		for _, column := range st.Schema.DBNames {
			f := st.Schema.LookUpField(column)
			if f == nil || f.AutoUpdateTime == 0 || m[f.Name] != nil || m[f.DBName] != nil ||
				targetReaches(st, clause.Column{Name: f.DBName}, field) != tenantColumn {
				continue
			}
			if sel, explicit := selected[f.DBName]; !explicit || sel {
				out = append(out, st.NowFunc())
			}
		}
		return out
	}
	if updating.Kind() != reflect.Struct {
		return nil
	}
	sameValue := updating.CanAddr() && samePointer(st.Dest, st.Model)
	schema := st.Schema
	if !sameValue {
		parsed := &gorm.Statement{DB: st.DB}
		if err := parsed.Parse(st.Dest); err == nil {
			schema = parsed.Schema
		}
	}
	var out []any
	for _, column := range st.Schema.DBNames {
		own := schema.LookUpField(column)
		if own == nil || (own.PrimaryKey && sameValue) ||
			targetReaches(st, clause.Column{Name: own.DBName}, field) != tenantColumn {
			continue
		}
		sel, explicit := selected[own.DBName]
		stamped := !st.SkipHooks && own.AutoUpdateTime > 0
		if inSelection := (explicit && sel) || (!explicit && (!restricted || stamped)); !inSelection {
			continue
		}
		v, isZero := own.ValueOf(st.Context, updating)
		if stamped {
			// GORM assigns the update time in place of whatever the field holds.
			v, isZero = st.NowFunc(), false
		}
		if (explicit || !isZero) && own.Updatable {
			out = append(out, v)
		}
	}
	return out
}

func upsertValues(st *gorm.Statement, field *gormschema.Field) []any {
	c, ok := st.Clauses["ON CONFLICT"]
	if !ok {
		return nil
	}
	conflict, ok := c.Expression.(clause.OnConflict)
	if !ok {
		return nil
	}
	var out []any
	for _, a := range conflict.DoUpdates {
		switch targetReaches(st, a.Column, field) {
		case elsewhere:
			continue
		case unread:
			out = append(out, unreadTarget{name: a.Column.Name})
			continue
		}
		if col, ok := a.Value.(clause.Column); ok && !col.Raw &&
			col.Table == excludedTable && col.Name == field.DBName {
			continue
		}
		out = append(out, a.Value)
	}
	return out
}

// excludedTable is the name GORM's conflict clause reads the row to insert under.
const excludedTable = "excluded"

type reach int

const (
	elsewhere reach = iota
	tenantColumn
	unread
)

func keyReaches(st *gorm.Statement, key string, field *gormschema.Field) bool {
	return targetReaches(st, clause.Column{Name: keyColumn(st, key)}, field) == tenantColumn
}

func keyColumn(st *gorm.Statement, key string) string {
	if f := st.Schema.LookUpField(key); f != nil {
		return f.DBName
	}
	return key
}

func targetReaches(st *gorm.Statement, col clause.Column, field *gormschema.Field) reach {
	name := col.Name
	if name == clause.PrimaryKey {
		name = primaryColumn(st.Schema)
	} else if f := st.Schema.LookUpField(name); f != nil && f.DBName == field.DBName {
		return tenantColumn
	}
	if !col.Raw {
		if sameIdentifier(name[strings.LastIndexByte(name, '.')+1:], field.DBName) {
			return tenantColumn
		}
		return elsewhere
	}
	parts, ok := columnParts(name)
	switch {
	case !ok:
		return unread
	case sameIdentifier(parts[len(parts)-1], field.DBName):
		return tenantColumn
	}
	return elsewhere
}

func primaryColumn(s *gormschema.Schema) string {
	if s.PrioritizedPrimaryField != nil {
		return s.PrioritizedPrimaryField.DBName
	}
	if len(s.DBNames) > 0 {
		return s.DBNames[0]
	}
	return ""
}

func sameIdentifier(written, column string) bool {
	s := strings.TrimSpace(written)
	if len(s) >= 2 {
		if closing, ok := quoteClosing(s[0]); ok && s[len(s)-1] == closing {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return strings.EqualFold(s, column)
}

func quoteClosing(c byte) (byte, bool) {
	switch c {
	case '"', '`':
		return c, true
	case '[':
		return ']', true
	}
	return 0, false
}

func columnParts(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	var parts []string
	for {
		part, rest, ok := columnPart(s)
		if !ok {
			return nil, false
		}
		parts = append(parts, part)
		if rest == "" {
			return parts, true
		}
		if rest[0] != '.' {
			return nil, false
		}
		s = rest[1:]
	}
}

func columnPart(s string) (part, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if closing, quoted := quoteClosing(s[0]); quoted {
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != closing {
				b.WriteByte(s[i])
				continue
			}
			if closing != ']' && i+1 < len(s) && s[i+1] == closing {
				b.WriteByte(closing)
				i++
				continue
			}
			return b.String(), s[i+1:], true
		}
		return "", "", false
	}
	end := 0
	for end < len(s) && isNameByte(s[end], end == 0) {
		end++
	}
	if end == 0 {
		return "", "", false
	}
	return s[:end], s[end:], true
}

func isNameByte(c byte, first bool) bool {
	switch {
	case c == '_' || c >= 0x80 || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z'):
		return true
	case ('0' <= c && c <= '9') || c == '$':
		return !first
	}
	return false
}

type unreadTarget struct{ name string }

func (u unreadTarget) refusal(st *gorm.Statement) error {
	return fmt.Errorf("%w: %s assigns to %q, which cannot be read as one column and may be its "+
		"tenant column; name the column alone", tenant.ErrExplicitTenantMismatch, st.Schema.Name, u.name)
}

func assigned(selected map[string]bool, restricted bool, column string) bool {
	sel, explicit := selected[column]
	return (explicit && sel) || (!explicit && !restricted)
}

func isNull(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

func describe(v any) string {
	if isNull(v) {
		return "a null"
	}
	return fmt.Sprintf("a %T", v)
}

func stringValue(v reflect.Value) (string, bool) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.String {
		return "", false
	}
	return v.String(), true
}

func conventionFieldOf(s *gormschema.Schema) (*gormschema.Field, error) {
	if s == nil {
		return nil, nil
	}
	var convention *gormschema.Field
	for _, f := range s.Fields {
		if !isConvention(s.ModelType, f) {
			continue
		}
		switch {
		case convention == nil:
			convention = f
		case f.DBName != convention.DBName:
			return nil, fmt.Errorf("%w: model %s embeds tenant.Model on two columns, %s and %s, and a "+
				"scoped model has one tenant column; embed the convention once", tenant.ErrNoTenantInContext,
				s.Name, convention.DBName, f.DBName)
		}
	}
	if convention != nil {
		return s.FieldsByDBName[convention.DBName], nil
	}
	if path, ok := unreadConvention(s.ModelType, nil); ok {
		return nil, fmt.Errorf("%w: model %s embeds tenant.Model through %s, which GORM does not read — "+
			"an unexported struct or field, or one tagged to be ignored — so the model carries no tenant "+
			"column and no statement through it can be given one; embed the convention where GORM reads it",
			tenant.ErrNoTenantInContext, s.Name, path)
	}
	return nil, nil
}

func isConvention(modelType reflect.Type, f *gormschema.Field) bool {
	return f.StructField.Name == conventionField && embedsConvention(modelType, f.StructField.Index)
}

func embedsConvention(modelType reflect.Type, path []int) bool {
	t := modelType
	for _, step := range path[:max(len(path)-1, 0)] {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return false
		}
		if step < 0 {
			step = -step - 1
		}
		if step >= t.NumField() {
			return false
		}
		t = t.Field(step).Type
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return len(path) > 1 && t == conventionType
}

func unreadConvention(t reflect.Type, visiting []reflect.Type) (string, bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || slices.Contains(visiting, t) {
		return "", false
	}
	visiting = append(visiting, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.Anonymous && !taggedEmbedded(f.Tag.Get("gorm")) {
			continue
		}
		inner := f.Type
		for inner.Kind() == reflect.Pointer {
			inner = inner.Elem()
		}
		if inner == conventionType {
			return f.Name, true
		}
		if path, ok := unreadConvention(inner, visiting); ok {
			return f.Name + "." + path, true
		}
	}
	return "", false
}

// embeddedSetting is the key GORM reads a field's embedded setting under.
const embeddedSetting = "EMBEDDED"

func taggedEmbedded(tag string) bool {
	if !containsFold(tag, embeddedSetting) {
		return false
	}
	_, ok := gormschema.ParseTagSetting(tag, ";")[embeddedSetting]
	return ok
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func noTenant(st *gorm.Statement, field *gormschema.Field, r reached) error {
	name := "model a scoped model"
	switch {
	case field != nil:
		name = "model " + st.Schema.Name
	case len(r.joins) > 0:
		name = "model " + r.joins[0].model
	case len(r.from) > 0:
		name = "table " + r.from[0].table
	}
	return fmt.Errorf("%w: %s", tenant.ErrNoTenantInContext, name)
}
