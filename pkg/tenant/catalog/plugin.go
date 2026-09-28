package catalog

import (
	"fmt"
	"reflect"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormschema "gorm.io/gorm/schema"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// callbackName identifies the callbacks this plugin registers, in GORM's own
// registry.
const callbackName = "tenant:scope"

// conventionField is the Go field tenant.Model contributes. GORM flattens an
// embedded struct, so the field appears on the model's own schema under this
// name. The column it lands in is read off the field, not assumed.
const conventionField = "TenantID"

// conventionType is the embedded type that marks a model as scoped. Comparing
// against it is what distinguishes a model that embedded the convention from
// one that happens to carry a field of the same name.
var conventionType = reflect.TypeOf(tenant.Model{})

// whereKey is the clause this plugin reads and replaces. It is written out
// rather than taken from clause.Where{}.Name() so that a reader sees the one
// key this package touches.
const whereKey = "WHERE"

// filter scopes statements to the tenant their context carries.
//
// It is deliberately without state. The instance is frozen with the descriptor
// at init time, database hands that one instance to every handle it builds, and
// a process driving two commands builds two registries that share it — so
// anything learned per run would be the other registry's value on the second
// run. Everything this plugin judges comes from the model GORM has already
// parsed and from the statement's context, and it keeps neither.
//
// It also does not consult the roster, so it cannot tell a tenant that does not
// exist from one that was disabled from one that was simply never put in the
// context. Those three arrive as one thing: no identifier. Refusing statements
// without one is the whole of what this side enforces, and disabling a tenant
// takes effect on the request face because that is where the roster is read.
type filter struct{}

// newFilter builds the plugin the descriptor declares.
func newFilter() *filter { return &filter{} }

// Name identifies this plugin in GORM's own registry of plugins, which is what
// database reports when two declarations collide. The convention there is the
// package path.
func (f *filter) Name() string { return "github.com/vislake/speed/pkg/tenant/catalog" }

// Initialize registers the callbacks on one handle.
//
// The handle is used and not kept. GORM calls this once per handle, and
// database hands the same plugin instance to the handles it builds later too —
// a plugin that captured the first handle would be silent on the second, which
// is the repeated-initialisation advice database states and this plugin keeps.
//
// Reads, updates and deletes all carry a condition; an insert carries a value
// instead. Registering before each processor's own step is what puts the
// condition into the clause the step renders, and the value into the row it
// binds.
func (f *filter) Initialize(handle *gorm.DB) error {
	registered := []func(*gorm.DB) error{
		func(h *gorm.DB) error {
			return h.Callback().Query().Before("gorm:query").Register(callbackName, f.scopeRead)
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

// scope is what the plugin makes of one statement.
type scope int

const (
	// unscoped is a statement against a model that does not embed the
	// convention. The plugin leaves it alone.
	unscoped scope = iota
	// crossing is a scoped statement whose context crosses tenants. It is
	// neither conditioned nor filled, and what it writes into the tenant
	// column is still judged.
	crossing
	// refused is a scoped statement whose context carries no tenant. The
	// refusal is already on the statement.
	refused
	// tenanted is a scoped statement whose context carries a tenant.
	tenanted
)

// scopeOf settles whether this statement is this plugin's business, and
// returns the convention's field and the tenant the statement belongs to when
// it has one.
//
// The answers are ordered, and the order is load-bearing. Recognising the model
// comes first, so a statement that is not scoped is left alone whatever its
// context says. The cross-tenant mark comes next, so a statement that means to
// cross is not refused for lacking the very thing it means to lack. Only then
// is the identifier looked for.
//
// A statement that is scoped but not to be filtered gives up any condition an
// earlier execution of it left behind: a chain first run under one tenant and
// then carried to a crossing context, or to none, is judged by the context it
// runs under now.
func scopeOf(tx *gorm.DB, st *gorm.Statement) (*gormschema.Field, string, scope) {
	if !scoped(st) {
		return nil, "", unscoped
	}
	field := st.Schema.LookUpField(conventionField)
	if tenantctx.CrossTenant(st.Context) {
		releaseCondition(st)
		return field, "", crossing
	}
	id, ok := tenantctx.ID(st.Context)
	if !ok {
		releaseCondition(st)
		// The return value is the statement's accumulated error, which the
		// statement already holds; the caller reads it back through Error.
		_ = tx.AddError(noTenant(st))
		return field, "", refused
	}
	return field, id, tenanted
}

// scopeRead adds the tenant condition to a read.
func (f *filter) scopeRead(tx *gorm.DB) {
	st := tx.Statement
	field, id, s := scopeOf(tx, st)
	if s != tenanted {
		return
	}
	applyCondition(st, field.DBName, id, false)
}

// scopeDelete adds the tenant condition to a delete, and writes the tenant into
// the key a delete addressed by a struct derives its identity from.
//
// The condition is one GORM does not count as the caller's: a delete that
// carries nothing else is refused as GORM refuses it, rather than reaching
// every row of the tenant, unless the caller asked for a global delete.
func (f *filter) scopeDelete(tx *gorm.DB) {
	st := tx.Statement
	field, id, s := scopeOf(tx, st)
	if s != tenanted {
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
	applyCondition(st, field.DBName, id, !tx.AllowGlobalUpdate)
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
// The model an update is addressed by is keyed like a delete's, and the
// condition is discounted the same way.
func (f *filter) scopeUpdate(tx *gorm.DB) {
	st := tx.Statement
	field, id, s := scopeOf(tx, st)
	switch s {
	case crossing:
		if err := judgeEach(assignedValues(st, field), someTenant(st)); err != nil {
			_ = tx.AddError(err)
		}
		return
	case tenanted:
	default:
		return
	}
	if err := judgeEach(assignedValues(st, field), ownTenant(st, id)); err != nil {
		_ = tx.AddError(err)
		return
	}
	if err := fillKey(st, field, id, st.ReflectValue); err != nil {
		_ = tx.AddError(err)
		return
	}
	applyCondition(st, field.DBName, id, !tx.AllowGlobalUpdate)
}

// fillColumn writes the tenant into the rows an insert is about to bind, and
// judges what the rows and an upsert's assignments already carry there.
//
// A condition cannot do this job: it decides which rows a statement reaches,
// and an insert reaches none. Without this the column would be left to the
// caller, and a caller that forgot would bind an empty identifier, which the
// identifier refuses: the statement would fail rather than land in the tenant
// its context carries. An insert crossing tenants is not filled, and each of
// its rows has to name its tenant itself.
func (f *filter) fillColumn(tx *gorm.DB) {
	st := tx.Statement
	field, id, s := scopeOf(tx, st)
	var judge func(any) error
	switch s {
	case tenanted:
		judge = ownTenant(st, id)
	case crossing:
		judge = someTenant(st)
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

// scopeTerm is the tenant equality this plugin puts into a statement's WHERE.
//
// It is a type of its own rather than a clause.Eq, for two reasons. The first
// is that it has to be recognisable. A statement executed a second time — a
// chain that counted and then read, a clone carried to another context — still
// holds the term its first execution left, and the plugin replaces its own term
// rather than stacking a second one or leaving a stale tenant in force. The term
// carries the caller's expression list as it stood before it was grouped, so
// replacing it restores exactly what the caller wrote.
//
// The second is that it renders after GORM has added the conditions of its
// own — the key of a struct a delete or an update is addressed by, the
// soft-delete predicate — which is the only moment at which the question GORM
// asks of such a write, whether anything narrows it, can be asked with this
// plugin's condition set aside. GORM asks it after the statement is built and
// would find this term there; asking it here, the way GORM asks it, keeps
// GORM's refusal of an unconditioned write a refusal rather than letting the
// tenant condition turn it into a write over the whole tenant.
type scopeTerm struct {
	column string
	id     string
	// written is the caller's expression list as it stood before the term
	// was put beside it.
	written []clause.Expression
	// requireCondition is set on an update or a delete that did not ask for a
	// global write.
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

// narrowed reports whether the statement being rendered carries a condition
// besides this plugin's, counted the way GORM counts one.
func narrowed(builder clause.Builder) bool {
	st, ok := builder.(*gorm.Statement)
	if !ok {
		// Only a statement is ever handed in; anything else is not a write
		// this can judge, and it is not refused on a guess.
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

// applyCondition puts the tenant equality into the statement's WHERE, with
// everything the caller already wrote kept whole beside it.
//
// Appending the equality to the expression list is not enough, and the
// difference is a cross-tenant read rather than a question of style. GORM
// renders that list joined by AND, except that an OR the caller wrote stays a
// connector — so appending produces `a OR b AND tenant = ?`, and AND binds
// tighter, which leaves the whole `a` branch unscoped. Grouping what the caller
// wrote first makes the rendering `(a OR b) AND tenant = ?` whatever shape that
// expression takes.
func applyCondition(st *gorm.Statement, column, id string, requireCondition bool) {
	written := writtenConditions(st)
	exprs := make([]clause.Expression, 0, 2)
	if group := grouped(written); group != nil {
		exprs = append(exprs, group)
	}
	exprs = append(exprs, scopeTerm{
		column: column, id: id, written: written, requireCondition: requireCondition,
	})
	if st.Clauses == nil {
		st.Clauses = map[string]clause.Clause{}
	}
	c := st.Clauses[whereKey]
	c.Name = whereKey
	c.Expression = clause.Where{Exprs: exprs}
	st.Clauses[whereKey] = c
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

// carriesTerm reports whether this plugin's term is in the expression list.
func carriesTerm(where clause.Where) bool {
	for _, e := range where.Exprs {
		if _, ours := e.(scopeTerm); ours {
			return true
		}
	}
	return false
}

// grouped renders the caller's expression list as one expression that means
// what GORM's own rendering of that list as a WHERE means, inside parentheses.
//
// Two steps of GORM's rendering are repeated here, because rendering the list
// as anything but a WHERE skips them. A list holding one AND group is that
// group's contents. And a list whose first expression is a lone OR is rendered
// with the first expression that is not one moved in front of it, so that
// `Or(a).Where(b)` reads `b OR a`; without the move the same list reads
// `a AND b`, which is a different question.
//
// The group stays an AND group wherever it can, because GORM reads the
// conditions of a WHERE back — FirstOrCreate and FirstOrInit copy its
// equalities onto the value they return — and it looks inside an AND group and
// nowhere else. An AND group of more than one expression renders its own
// parentheses. A single expression gets explicit ones, unless it is an
// equality on a column, which cannot carry an OR and is the one shape those
// callers read back.
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
		return clause.Expr{SQL: "(?)", Vars: []interface{}{exprs[0]}}
	default:
		return clause.AndConditions{Exprs: exprs}
	}
}

// rawColumn reports whether a column is written into the SQL as it stands,
// which is the one way an equality's left side could carry an OR.
func rawColumn(column interface{}) bool {
	c, ok := column.(clause.Column)
	return ok && c.Raw
}

// fillKey writes the context's tenant into the key of the values an update or a
// delete derives its identity from, and refuses a key naming another tenant.
//
// GORM addresses a write by a struct's primary key, and under the composite key
// the convention declares, a struct whose tenant half is unwritten names a row
// of no tenant: the write would reach nothing and report success. Filling that
// half is the insert's fill carried over to the key. It is done only where some
// other part of the key is written, because a key that is all zero addresses
// nothing, and filling its tenant half would turn it into one addressing the
// whole tenant.
func fillKey(st *gorm.Statement, field *gormschema.Field, id string, values ...reflect.Value) error {
	for _, rv := range values {
		for _, v := range structValues(rv, st.Schema.ModelType) {
			written, isZero := field.ValueOf(st.Context, v)
			if !isZero {
				if s, ok := stringValue(reflect.ValueOf(written)); !ok || s != id {
					return fmt.Errorf("%w: %s is addressed by a key naming tenant %v",
						tenant.ErrExplicitTenantMismatch, st.Schema.Name, written)
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

// otherKeyWritten reports whether any primary field besides the tenant's holds
// a value.
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

// structValues lists the values of the model's type a reflected value holds: the
// value itself, or each element of a slice or an array, through any pointers.
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

// indirect follows pointers and interfaces to the value they hold, returning
// the zero Value where one of them is nil.
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

// ownTenant judges a value written under a tenant: it has to be that tenant's
// identifier.
//
// Crossing tenants is a context fact with one way in. Letting a statement name
// a second tenant would be a second way in, and one that carries no mark of
// intent: a write copied from elsewhere and edited would land in another tenant
// with nothing said. A value equal to the context's is not refused — it is the
// same statement written twice. Anything this cannot read as a string — an
// expression, a subquery, a null — is refused rather than trusted, because the
// question is whether it is the context's tenant, and nothing here can answer
// that for it.
func ownTenant(st *gorm.Statement, id string) func(any) error {
	return func(v any) error {
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

// someTenant judges a value written across tenants: whatever tenant it names,
// it has to name one.
//
// A row whose tenant column is empty, or null, belongs to no tenant, and no
// scoped statement can reach it again. A literal outside the identifier's
// domain is no tenant either. An expression is the caller's own statement of
// what the column becomes, which crossing tenants allows.
func someTenant(st *gorm.Statement) func(any) error {
	return func(v any) error {
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

// judgeEach applies a judgement to every value, stopping at the first refusal.
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
// written by key, and a key naming the column is found the way GORM finds it;
// a map that names none gets the column added. Anything else is read through
// the value GORM resolved the destination to, which is what makes a struct, a
// slice of structs and a slice of pointers alike — whether handed over by
// pointer or by value — and a struct or an array handed over by value is
// copied first so that there is somewhere to write.
func writeRows(st *gorm.Statement, field *gormschema.Field, id string, fill bool, judge func(any) error) error {
	if maps, ok := insertedMaps(st.Dest); ok {
		for _, m := range maps {
			if err := writeMap(st, field, m, id, fill, judge); err != nil {
				return err
			}
		}
		return nil
	}
	if fill {
		makeAddressable(st)
	}
	for _, v := range structValues(st.ReflectValue, st.Schema.ModelType) {
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

// writeMap fills and judges the tenant column of one row an insert binds from a
// map.
//
// Adding the column writes into the caller's map, as filling a struct writes
// into the caller's struct.
func writeMap(st *gorm.Statement, field *gormschema.Field, m map[string]interface{},
	id string, fill bool, judge func(any) error,
) error {
	if m == nil {
		return nil
	}
	selected, restricted := st.SelectAndOmitColumns(true, false)
	named := false
	for key, v := range m {
		if !namesColumn(st.Schema, key, field) {
			continue
		}
		named = true
		if !assigned(selected, restricted, field.DBName) {
			continue
		}
		if err := judge(v); err != nil {
			return err
		}
	}
	switch {
	case named:
		return nil
	case !fill:
		return unnamedAcross(st)
	}
	m[field.DBName] = id
	return nil
}

// unnamedAcross is the refusal of a row inserted across tenants that names no
// tenant at all.
func unnamedAcross(st *gorm.Statement) error {
	return fmt.Errorf("%w: %s is inserted across tenants with no tenant in its tenant column",
		tenant.ErrNoTenantInContext, st.Schema.Name)
}

// insertedMaps returns the maps an insert reads its rows from, for the four
// destination types GORM reads as maps.
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
// An explicit SET clause is used as it stands, and the destination is then not
// read at all. A map names its columns by key, resolved against the model's
// schema; a struct is read through its own schema, which is the model's or,
// for an update carrying its assignments in another type, that type's. A key
// field of the very value the update is addressed by is a condition rather
// than an assignment, and the key is judged where the key is filled. Selection
// and omission decide which of the rest are assigned, zero values included
// where they are selected.
func assignedValues(st *gorm.Statement, field *gormschema.Field) []any {
	if c, ok := st.Clauses["SET"]; ok {
		set, _ := c.Expression.(clause.Set)
		var out []any
		for _, a := range set {
			if namesColumn(st.Schema, a.Column.Name, field) {
				out = append(out, a.Value)
			}
		}
		return out
	}
	updating := indirect(reflect.ValueOf(st.Dest))
	if !updating.IsValid() {
		return nil
	}
	selected, restricted := st.SelectAndOmitColumns(false, true)
	if m, ok := updating.Interface().(map[string]interface{}); ok {
		var out []any
		for key, v := range m {
			if namesColumn(st.Schema, key, field) && assigned(selected, restricted, field.DBName) {
				out = append(out, v)
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
	own := schema.LookUpField(field.DBName)
	if own == nil || (own.PrimaryKey && sameValue) {
		return nil
	}
	sel, explicit := selected[own.DBName]
	inSelection := (explicit && sel) ||
		(!explicit && (!restricted || (!st.SkipHooks && own.AutoUpdateTime > 0)))
	if !inSelection {
		return nil
	}
	v, isZero := own.ValueOf(st.Context, updating)
	if (!explicit && isZero) || !own.Updatable {
		return nil
	}
	return []any{v}
}

// upsertValues lists what an insert's conflict clause assigns to the tenant
// column on the row it conflicts with. Reassigning the column from the row that
// was about to be inserted is not an assignment of a tenant: that row's own
// column is judged as the insert's.
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
		if !namesColumn(st.Schema, a.Column.Name, field) {
			continue
		}
		if col, ok := a.Value.(clause.Column); ok && strings.EqualFold(col.Table, excludedTable) &&
			namesColumn(st.Schema, col.Name, field) {
			continue
		}
		out = append(out, a.Value)
	}
	return out
}

// excludedTable is the name a conflict clause reads the row it was about to
// insert under, which is how GORM writes an assignment from that row.
const excludedTable = "excluded"

// namesColumn reports whether a key names the convention's column, resolved
// the way GORM resolves an assignment's key: through the model's schema, and as
// the column name where the schema has no field by that key.
func namesColumn(schema *gormschema.Schema, key string, field *gormschema.Field) bool {
	if f := schema.LookUpField(key); f != nil {
		return f.DBName == field.DBName
	}
	return key == field.DBName
}

// assigned reports whether selection and omission leave a column assigned.
func assigned(selected map[string]bool, restricted bool, column string) bool {
	sel, explicit := selected[column]
	return (explicit && sel) || (!explicit && !restricted)
}

// isNull reports whether a value binds as a null.
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

// describe names what a value is for a refusal's message, without its content.
func describe(v any) string {
	if isNull(v) {
		return "a null"
	}
	return fmt.Sprintf("a %T", v)
}

// stringValue unwraps a value to a string, reporting whether it was one.
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

// scoped reports whether this statement is against a scoped model.
//
// The check is on the model type the schema was parsed from rather than on the
// column it happens to have, so a model carrying a field named like the
// convention's is not swept up by it. What makes a model scoped is embedding
// the convention, and nothing else is allowed to mean that.
func scoped(st *gorm.Statement) bool {
	if st.Schema == nil || st.Schema.LookUpField(conventionField) == nil {
		return false
	}
	return embedsConvention(st.Schema.ModelType)
}

// embedsConvention walks the model's own fields looking for the convention as
// an embedded type.
func embedsConvention(modelType reflect.Type) bool {
	for modelType.Kind() == reflect.Pointer {
		modelType = modelType.Elem()
	}
	if modelType.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < modelType.NumField(); i++ {
		f := modelType.Field(i)
		if f.Anonymous && f.Type == conventionType {
			return true
		}
	}
	return false
}

// noTenant names the model a refused statement was against. The model is what
// the caller has to look at to see why no tenant reached it, and it is the one
// thing this side knows.
func noTenant(st *gorm.Statement) error {
	name := "a scoped model"
	if st.Schema != nil {
		name = st.Schema.Name
	}
	return fmt.Errorf("%w: model %s", tenant.ErrNoTenantInContext, name)
}
