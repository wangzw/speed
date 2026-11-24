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

// filter scopes statements to the tenant their context carries.
//
// It is deliberately without state: one instance is shared by every handle and
// by two registries in a process driving two commands, so it keeps nothing. It
// also does not consult the roster, so an unknown tenant, a disabled one and a
// missing one all arrive as no identifier, which is all this side refuses.
type filter struct{}

func newFilter() *filter { return &filter{} }

// Name identifies this plugin in GORM's own registry of plugins, which is what
// database reports when two declarations collide. The convention there is the
// package path.
func (f *filter) Name() string { return "github.com/vislake/speed/pkg/tenant/catalog" }

// Initialize registers the callbacks on one handle, which is used and not kept because GORM calls
// it once per handle. Reads, updates and deletes carry a condition and an insert a value, so each
// is registered before its processor's own step; a read goes through both processors.
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
	// refused is a scoped statement whose context carries no tenant, or a
	// statement through a model that embeds the convention where no one
	// tenant column can be taken from it. The refusal comes back beside it,
	// for the caller to put on the statement in the form that statement can
	// report.
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

// scopeOf settles whether this statement is this plugin's business. The order is
// load-bearing: recognising the model first, then the cross-tenant mark, then
// the identifier. A scoped statement not to be filtered gives up a condition an
// earlier execution left behind. A read of an unscoped model is still scoped
// when a join reaches a scoped model or a table carrying tenants.
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

func handsBackOneRow(tx *gorm.DB) bool {
	setting, ok := tx.Get("rows")
	many, isBool := setting.(bool)
	return ok && isBool && !many
}

// read scopes one read, or refuses it in the form it can report. The tenant half
// of a key in the association mode's shape is filled from the context, in the
// WHERE and in a FROM join's ON, whoever wrote the tuple; every other condition
// on the tenant column is left as written.
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

// refuseRead fails a read that has no tenant to be scoped to. Two reads cannot
// report a refusal put on the statement: a subquery has its error dropped by
// GORM, so it renders a select of one value whose binding fails; a Row has no
// error to return, so it is given a row whose Scan and Err report the refusal,
// which reaches no database and takes no connection.
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

// Value implements driver.Valuer.
func (r refusal) Value() (driver.Value, error) { return nil, r.err }

// refusedRow is a row whose Scan and Err report err, and which reaches no
// database.
//
// Only database/sql makes a *sql.Row that carries an error, and it makes one
// when opening the result set fails. So the row is asked of a pool of this
// package's own whose connector refuses every connection with err: the query
// fails before it reaches a connection, and the row carries err as it was
// given. The pool holds nothing once the row is made, and it is closed then.
func refusedRow(err error) *sql.Row {
	pool := sql.OpenDB(refusingConnector{err: err})
	defer func() { _ = pool.Close() }()
	return pool.QueryRowContext(context.Background(), "")
}

type refusingConnector struct{ err error }

// Connect refuses the connection with the connector's error.
func (c refusingConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }

// Driver is the connector's driver, which opens nothing either.
func (c refusingConnector) Driver() driver.Driver { return refusingDriver(c) }

type refusingDriver struct{ err error }

// Open refuses the connection with the driver's error.
func (d refusingDriver) Open(string) (driver.Conn, error) { return nil, d.err }

// scopedJoin is one association join of a read that reaches a scoped model. A
// nested join renders one ON clause per level, keyed by the alias GORM gives that
// level's table; an unscoped level renders a condition that holds for every row,
// and an unforeseen alias renders the tenant condition.
type scopedJoin struct {
	index    int
	model    string
	columns  map[string]string
	unscoped map[string]bool
	fallback string
}

// scopedJoins lists the association joins of this read that reach a scoped
// model. A join GORM renders from the caller's own expression, or a join
// written as SQL text, names its tables in text the plugin does not parse, and
// is not among them. A join reaching a model that embeds the convention where
// no tenant column can be taken from it returns that model's refusal.
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

// Build renders the condition for the level whose ON clause is being built:
// GORM builds each level's ON clause on a statement named after that level's
// alias.
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

// scopeDelete adds the tenant condition to a delete, and writes the tenant into
// the key a delete addressed by a struct derives its identity from.
//
// The condition is one GORM does not count as the caller's: a delete that
// carries nothing else is refused as GORM refuses it, rather than reaching
// every row of the tenant, unless the caller asked for a global delete.
//
// A key GORM has already written into the delete's conditions — the
// association mode's, which addresses the links of a record by the record's
// whole key — gets its tenant half filled the same way, and a condition naming
// another tenant on the tenant column, that key's or the caller's, is refused
// as a struct's key naming one is.
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

// scopeUpdate adds the condition and judges what the update assigns to the
// tenant column.
//
// The judgement belongs here as well as beside the insert's, because an update
// states which columns to set and the tenant column is one of them. Leaving it
// unexamined lets a statement matched against one tenant's row move that row
// into another: the condition decides which rows are reached, and the value
// they are then given carries them away. An update crossing tenants is not
// conditioned, and may move a row on purpose, but it may not empty the column.
//
// The model an update is addressed by is keyed like a delete's, a key already
// in its conditions is filled and judged like a delete's, and the condition is
// discounted the same way.
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

// scopeTerm is the tenant equality this plugin puts into a statement's WHERE. It
// is its own type so it can be recognised and replaced when a statement is
// executed again, and so it renders after GORM's own conditions — letting GORM's
// refusal of an unconditioned write stay a refusal.
type scopeTerm struct {
	column           string
	id               string
	written          []clause.Expression
	requireCondition bool
}

// Build renders the equality, refusing an unconditioned write on the way.
func (t scopeTerm) Build(builder clause.Builder) {
	if t.requireCondition && !narrowed(builder) {
		_ = builder.AddError(gorm.ErrMissingWhereClause)
	}
	clause.Eq{
		Column: clause.Column{Table: clause.CurrentTable, Name: t.column},
		Value:  t.id,
	}.Build(builder)
}

// softDeleteKey is the marker GORM leaves on a statement whose model soft
// deletes, after it has added the predicate that hides deleted rows. GORM does
// not count that predicate as a condition of the caller's, and neither does
// narrowed.
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

// applyCondition puts the tenant equality into the statement's WHERE, with the
// caller's expression list grouped whole beside it: appending produces
// `a OR b AND tenant = ?`, which leaves the `a` branch unscoped. Key-shaped
// tuples on the tenant column are filled from the context; other conditions are
// rendered as written, and the other tenants they name are returned for a write
// to refuse.
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

// writtenConditions is the caller's own expression list: the statement's WHERE
// with any term this plugin put there taken back out, and whatever was added
// after the term — a condition chained on after an execution, or one GORM
// derived during it — kept after the caller's.
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

// releaseCondition takes this plugin's term back out of a statement that is no
// longer to be filtered, leaving the caller's WHERE as the caller wrote it — or
// no WHERE at all where the caller wrote none, so that GORM's own refusal of an
// unconditioned write sees the statement as the caller left it.
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

// grouped renders the caller's expression list as GORM's own WHERE rendering
// would, inside parentheses. It repeats two of GORM's steps: a list holding one
// AND group is that group's contents, and a leading lone OR moves behind the
// first other expression. The group stays an AND group wherever possible,
// because GORM reads a WHERE's conditions back only inside one.
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

// Build renders the expression as it renders itself, between parentheses.
func (p parenthesized) Build(builder clause.Builder) {
	_ = builder.WriteByte('(')
	p.expr.Build(builder)
	_ = builder.WriteByte(')')
}

func rawColumn(column interface{}) bool {
	c, ok := column.(clause.Column)
	return ok && c.Raw
}

// fillKey writes the context's tenant into the key of the values an update or a
// delete derives its identity from, and refuses a key naming another tenant.
//
// GORM addresses a write by a struct's primary key. A delete puts every part of
// the key into its condition, so under the composite key the convention
// declares, a struct whose tenant half is unwritten names a row of no tenant:
// the delete would reach nothing and report success. An update puts only the
// written parts there, and the fill keeps the value it is addressed by in step
// with the row it reaches. Filling that half is the insert's fill carried over
// to the key. It is done only where some
// other part of the key is written, because a key that is all zero addresses
// nothing, and filling its tenant half would turn it into one addressing the
// whole tenant.
//
// A tenant column bound to a field outside the key — a field of the model's own
// that takes the convention's column over without declaring it part of the
// key — is no part of what GORM addresses the write by, and is left alone here;
// what an update assigns to it is judged with the rest of the assignments.
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

// makeAddressable replaces a destination handed in by value with a pointer to
// a copy of it, so that the plugin can write into it.
//
// GORM accepts a struct or an array by value and reads the row from it; nothing
// can be written into such a value, so without the copy the tenant would have
// nowhere to go. The caller handed over a copy in the first place, and cannot
// observe the one made here. GORM set the model to the same value when the
// caller named no other, and it is moved along with the destination so that the
// two stay one value.
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

// samePointer reports whether two values are one pointer. It is how GORM's own
// comparison of a statement's model with its destination reads for the
// pointers it matters for, without comparing two values of a type that cannot
// be compared.
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

// writeRows fills and judges the tenant column of every row an insert binds.
//
// The rows are read from where GORM reads them. A map, or a slice of maps, is
// written by key, and a key is taken for the column it reaches in the database
// (keyReaches); a map that names none gets the column added. Anything else is
// read through the value GORM resolved the destination to, which is what makes
// a struct, a slice of structs and a slice of pointers alike — whether handed
// over by pointer or by value — and a struct or an array handed over by value is
// copied first so that there is somewhere to write.
//
// A field the insert writes, besides the one bound to the tenant column, whose
// column the database reads as the tenant column is another spelling of that
// column, and GORM writes it beside the tenant column; SQLite keeps the first
// value an insert gives one column, so the spelling declared first decides the
// row's tenant. What such a field binds is judged as the tenant column's value
// is. A field GORM does not write is not judged (insertWrites).
//
// A row GORM builds without the tenant column is refused before anything is
// filled: the statement's selection or omission leaves the column out, or the
// field bound to it is one GORM does not write on insert, and whatever the
// filter fills or the caller wrote then stays in the value and never reaches
// the table. The row would belong to no tenant where the column takes a null,
// and fail on the constraint where it does not.
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
			// Nothing can be written here; the identifier refuses to be
			// bound empty, so the insert still fails.
			continue
		}
		if err := field.Set(st.Context, v, id); err != nil {
			return err
		}
	}
	return nil
}

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

// insertWrites reports whether GORM builds an insert's row from this field.
//
// GORM writes each of the model's columns from the one field it binds the
// column to, and beside those, every field whose default is the database's and
// that holds a value — a field repeating another's column included. A field
// that repeats a column exactly and is neither is never written, and judging
// what it holds would refuse an insert over a value that never reaches the
// table.
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

// judgeSpellings judges what an insert binds, for one row, under each of these
// other spellings of the tenant column, read the way GORM builds the row: a
// field left out by selection or omission binds nothing, a zero value gives way
// to the field's default or its creation time, and a zero value of a field whose
// default is the database's binds nothing.
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

// insertSelects reports whether the row GORM builds for an insert from a struct
// carries a field's column, as GORM decides it: a column the statement selects,
// or any column when it selects none, and a column stamped with the creation or
// update time even when it selects others — unless the statement omits it, or
// the field is one GORM does not write on insert, which GORM's selection holds
// as an omission.
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

// writeMap fills and judges the tenant column of one row an insert binds from a
// map.
//
// Adding the column writes into the caller's map, as filling a struct writes
// into the caller's struct.
//
// A key's value is written, and judged, when GORM inserts it, which it decides
// under the column the key is written as (keyColumn). A map none of whose keys
// GORM writes to the tenant column gets the column added under a tenant, and is
// refused across tenants; one that selection or omission keeps the added column
// out of is refused either way, because the row would reach the table with no
// tenant.
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

// assignedValues lists what an update assigns to the tenant column, read the
// way GORM builds the assignments.
//
// GORM renders the assignments it derives from the destination whenever there
// are any, and a SET clause the caller wrote only when there are none. Both are
// read here: which of the two reaches the table depends on the destination, and
// the one left unread would be a way past the judgement. A SET clause's targets
// are read as the database reads them (targetReaches). A map names its columns
// by key (keyReaches); a struct is read the way GORM builds its assignments
// (destinationValues). A key field of the very value the update is addressed by
// is a condition rather than an assignment, and the key is judged where the key
// is filled. Selection and omission decide which of the rest are assigned, zero
// values included where they are selected.
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

// destinationValues lists what the destination of an update assigns to the
// tenant column.
//
// A map key's value is assigned when GORM's selection leaves it so, which it
// decides under the column the key is written as (keyColumn). GORM also stamps
// the update time into every field of the model that carries one and that the
// map leaves out, unless the selection leaves that field out, so a field doing
// that under a spelling of the tenant column assigns the time to it.
//
// A struct is read the way GORM builds assignments from one. For each of the
// model's columns GORM looks up a field of the updating type — the model's own,
// or another type's — under that column's name, which also finds a field whose
// Go name it is, and assigns the column that field carries. What reaches the
// tenant column is therefore whichever of those columns the database reads as
// it, rather than the field that happens to be named like it: a patch type
// whose field is found for a column named Note and tagged with another spelling
// of the tenant column writes that spelling.
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
			v, isZero = st.NowFunc(), false
		}
		if (explicit || !isZero) && own.Updatable {
			out = append(out, v)
		}
	}
	return out
}

// upsertValues lists what an insert's conflict clause assigns to the tenant
// column on the row it conflicts with, its targets read as the database reads
// them.
//
// Reassigning the column from the row that was about to be inserted is not an
// assignment of a tenant: that row's own column is judged as the insert's. Only
// the form GORM itself writes that reassignment in is taken for it — the
// column by its own name, under the name the conflict clause reads that row by.
// A spelling the database might read as some other column of that row is
// judged like any other value, because letting it through would copy that
// column into the tenant column.
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

const excludedTable = "excluded"

type reach int

const (
	elsewhere reach = iota
	tenantColumn
	unread
)

// keyReaches reports whether a key GORM builds an assignment or an inserted
// column from reaches the tenant column.
//
// GORM resolves the key through the model's schema first, and writes the
// field's own column when that succeeds. A key the schema does not know is not
// dropped: it goes into the statement as a column name. Either way the column
// written reaches whatever the database reads that name as — which, for a key
// spelled in another case or carrying its own quotes, or for a field of the
// model tagged with such a spelling, is the tenant column.
func keyReaches(st *gorm.Statement, key string, field *gormschema.Field) bool {
	return targetReaches(st, clause.Column{Name: keyColumn(st, key)}, field) == tenantColumn
}

// keyColumn is the column GORM writes a map key's value as, and the one it looks
// the key up under in the statement's selection: the column of the field the
// model's schema resolves the key to — by the field's Go name or by its
// column — or the key itself when the schema resolves none. A key naming a
// field of the model tagged with another spelling of the tenant column is
// therefore selected by any selection naming that field, however it names it.
func keyColumn(st *gorm.Statement, key string) string {
	if f := st.Schema.LookUpField(key); f != nil {
		return f.DBName
	}
	return key
}

// targetReaches reads the column an assignment is written to the way the
// database reads it.
//
// GORM writes a target into the statement quoted, part by part at each dot,
// unless it is marked raw, which writes it as it stands; its primary key
// placeholder is written as the column it stands for. The database then
// resolves the name by its own rules: SQLite compares names without regard to
// case, quoted or not; PostgreSQL folds a bare name to lower case; both skip
// the blanks around a name written as it stands, and both take the last part of
// a dotted name for the column. The comparison here is the widest of those
// readings — a name is the tenant column if any of them could make it so —
// because reading too widely costs a refusal of a write the database would
// have rejected anyway, and reading too narrowly costs a row carried into
// another tenant.
//
// A raw target that is not one column name, possibly qualified and quoted, is
// SQL the filter does not parse, and is unread. A name the model's schema
// resolves to a field on the tenant column is taken for that column as well,
// although GORM does not resolve a clause's column through the schema: the
// database rejects such a name if it is not the column, so reading it as the
// column refuses nothing that would have run.
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

// primaryColumn is the column GORM writes its primary key placeholder as: the
// field it prefers as the key, or the model's first column when it prefers
// none — which, for a model whose own key is not called id, is the tenant
// column the convention puts first.
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

// columnParts reads a column reference written into a statement as it stands:
// blanks around it, then one or more parts joined by dots, each a bare name or
// a name quoted with double quotes, backquotes or brackets, a doubled quote
// inside standing for one. It reports false for anything else — an
// expression, a list of assignments, a comment — which names no single column.
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

// conventionFieldOf returns the field GORM binds a scoped model's tenant column
// to, nil when the model is not scoped, or the refusal of a model that embeds
// the convention where no one tenant column can be taken from it.
//
// What makes a model scoped is embedding the convention, and nothing else is
// allowed to mean that. The tenant column is found by where it comes from: it
// is the column GORM parsed the convention's own field onto (isConvention),
// however the model embeds the convention — directly, by pointer, through an
// exported base struct, in a field tagged embedded — and whatever that column
// is called, since an embedded prefix renames it. Neither the column's name nor
// the convention's field name is looked up. A prefix moves the column off the
// name the convention declares, so a lookup by that name takes the model for a
// global table; and a field of the model's own named like the convention's
// takes that name, so a lookup by it finds that field and does the same. A
// model carrying a field named like the convention's, on the convention's
// column, without embedding the convention, is not swept up either.
//
// The field returned is the one GORM binds the column to, which need not be the
// convention's own. GORM binds each column to one field of the model — the one
// reached by the shortest path, the first declared among equals — and reads and
// writes that field alone, under its name and under the column's. A model that
// embeds the convention and declares a field of its own on the column, or
// reaches one first through another embedding, has that field read and written
// in place of the convention's. Such a model is still scoped, and taking the
// convention's field for the column would leave the filter judging a field
// GORM never writes.
//
// Two declarations leave no one tenant column to judge, and a model declared
// either way is refused rather than read as a global table. The convention
// embedded where GORM does not read it — through an unexported struct or field,
// or in a field tagged to be ignored — puts no field on the model, so GORM
// neither reads the column into the model nor writes it into a row
// (unreadConvention). The convention embedded twice on two columns names two,
// and a row belongs to one tenant.
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

// embedsConvention reports whether the field at this index path of the model is
// the convention's own, reached through embedding.
//
// Embedding is whatever GORM flattens into the model, and GORM flattens an
// embedded struct however it is reached: directly, through a base struct the
// model embeds, by pointer, or through a field tagged embedded. Each of those
// puts the convention's column on the model, so each declares it scoped; a
// check of the model's own fields alone would take every other one for a
// global table and read it across tenants. GORM records the path from the model
// to the flattened field, with a pointer step written as a negative index, and
// the field is the convention's when the struct the path ends in is.
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

// unreadConvention reports whether a struct embeds the convention anywhere
// along its embeddings, and the Go path of fields that reaches it. It is asked
// only of a model GORM parsed no convention field for, so an embedding it
// finds is one GORM does not read.
//
// Every embedding is followed, read by GORM or not — an anonymous field, or one
// tagged embedded, exported or not, by value or by pointer — because a base
// GORM reads can itself embed the convention through one it does not. The
// structs on the path so far are carried along, so that a struct embedding a
// pointer to itself ends the walk rather than repeating it.
//
// The walk reads the model's type rather than the schema GORM cached, which
// holds nothing of what GORM did not read. It runs for statements through a
// model that carries no convention field, and its cost is the number of fields
// along the model's embeddings.
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

// noTenant names the model a refused statement was against. The model is what
// the caller has to look at to see why no tenant reached it, and it is the one
// thing this side knows. A read refused for a scoped model it reaches through a
// join names that model, and one refused for a table carrying tenants that its
// FROM clause joins by name names that table.
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
