package catalog

import (
	"context"
	"fmt"
	"reflect"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormschema "gorm.io/gorm/schema"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// callbackName identifies the callbacks this plugin registers, in GORM's own
// registry.
const callbackName = "tenant:scope"

const (
	// conventionField is the Go field tenant.Model contributes. GORM
	// flattens an embedded struct, so the field appears on the model's own
	// schema under this name.
	conventionField = "TenantID"
	// columnName is where that field lands in the table.
	columnName = "tenant_id"
)

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
			return h.Callback().Query().Before("gorm:query").Register(callbackName, f.scopeWhere)
		},
		func(h *gorm.DB) error {
			return h.Callback().Update().Before("gorm:update").Register(callbackName, f.scopeUpdate)
		},
		func(h *gorm.DB) error {
			return h.Callback().Delete().Before("gorm:delete").Register(callbackName, f.scopeWhere)
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

// scopedTo settles whether this statement is this plugin's business, and
// returns the tenant it belongs to when it is.
//
// The answers are ordered, and the order is load-bearing. Recognising the model
// comes first, so a statement that is not scoped is left alone whatever its
// context says. The cross-tenant mark comes next, so a statement that means to
// cross is not refused for lacking the very thing it means to lack. Only then
// is the identifier looked for.
func scopedTo(tx *gorm.DB, st *gorm.Statement) (string, bool) {
	if !scoped(st) || tenantctx.CrossTenant(st.Context) {
		return "", false
	}
	id, ok := tenantctx.ID(st.Context)
	if !ok {
		// The return value is the statement's accumulated error, which the
		// statement already holds; the caller reads it back through Error.
		_ = tx.AddError(noTenant(st))
		return "", false
	}
	return id, true
}

// scopeWhere adds the tenant condition to a statement that carries a WHERE.
func (f *filter) scopeWhere(tx *gorm.DB) {
	id, ok := scopedTo(tx, tx.Statement)
	if !ok {
		return
	}
	applyCondition(tx.Statement, id)
}

// scopeUpdate adds the condition and refuses a write that names another tenant.
//
// The refusal belongs here rather than beside the insert's, because an update
// states which columns to set and the tenant column is one of them. Leaving it
// unexamined lets a statement matched against one tenant's row move that row
// into another: the condition decides which rows are reached, and the value
// they are then given carries them away.
func (f *filter) scopeUpdate(tx *gorm.DB) {
	st := tx.Statement
	id, ok := scopedTo(tx, st)
	if !ok {
		return
	}
	if written, ok := assignedTenant(st); ok && written != "" && written != id {
		_ = tx.AddError(fmt.Errorf("%w: %s is being set to %q",
			tenant.ErrExplicitTenantMismatch, st.Schema.Name, written))
		return
	}
	applyCondition(st, id)
}

// fillColumn writes the tenant into the rows an insert is about to bind.
//
// A condition cannot do this job: it decides which rows a statement reaches,
// and an insert reaches none. Without this the column would be left to the
// caller, and a caller that forgot would bind an empty identifier, which the
// identifier refuses: the statement would fail rather than land in the tenant
// its context carries.
func (f *filter) fillColumn(tx *gorm.DB) {
	st := tx.Statement
	id, ok := scopedTo(tx, st)
	if !ok {
		return
	}
	field := st.Schema.LookUpField(conventionField)
	for _, dest := range destinations(st.Dest) {
		if err := fillOne(st, field, dest, id); err != nil {
			_ = tx.AddError(err)
			return
		}
	}
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
func applyCondition(st *gorm.Statement, id string) {
	if st.Clauses == nil {
		st.Clauses = map[string]clause.Clause{}
	}
	st.Clauses[whereKey] = clause.Clause{
		Name:       whereKey,
		Expression: condition(st, id),
	}
}

// condition builds the WHERE this plugin imposes.
func condition(st *gorm.Statement, id string) clause.Where {
	tenantTerm := clause.Eq{
		Column: clause.Column{Table: clause.CurrentTable, Name: columnName},
		Value:  id,
	}
	written, ok := st.Clauses[whereKey].Expression.(clause.Where)
	if !ok || len(written.Exprs) == 0 {
		return clause.Where{Exprs: []clause.Expression{tenantTerm}}
	}
	// What is grouped is the caller's expression list, not its WHERE clause:
	// clause.Where writes the keyword itself, and nesting it here would put a
	// second WHERE inside the parentheses.
	//
	// The parentheses are written out rather than left to AndConditions,
	// because buildExprs flattens a group holding more than one expression into
	// the surrounding list and drops its parentheses — which is exactly the
	// case this has to survive. Where AndConditions does add its own, the
	// result is one redundant pair and no change in meaning.
	return clause.Where{Exprs: []clause.Expression{
		clause.Expr{SQL: "(?)", Vars: []interface{}{
			// AndConditions shares its one field with Where; what
			// differs is how each renders, and it is the list that
			// has to be rendered here, not the clause.
			clause.AndConditions(written),
		}},
		tenantTerm,
	}}
}

// fillOne writes the identifier onto one destination, refusing a value the
// caller wrote by hand.
//
// Crossing tenants is a context fact with one way in. Letting a statement name
// a second tenant would be a second way in, and one that carries no mark of
// intent: a write copied from elsewhere and edited would land in another tenant
// with nothing said. A value equal to the context's is not refused — it is the
// same statement written twice.
func fillOne(st *gorm.Statement, field *gormschema.Field, dest reflect.Value, id string) error {
	if written, ok := writtenTenant(st.Context, field, dest); ok && written != "" && written != id {
		return fmt.Errorf("%w: %s carries %q in its tenant column",
			tenant.ErrExplicitTenantMismatch, st.Schema.Name, written)
	}
	return field.Set(st.Context, dest, id)
}

// assignedTenant reads the tenant column an update is about to set, if it is
// about to set it at all.
//
// An update states its assignments in one of two shapes: a map, which is what
// Update and Updates with a map produce, or a struct, which is what Updates and
// Save produce. Both are read here, because a plugin that covered one would
// leave the same hole open through the other.
func assignedTenant(st *gorm.Statement) (string, bool) {
	rv := reflect.ValueOf(st.Dest)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		iter := rv.MapRange()
		for iter.Next() {
			key := iter.Key()
			if key.Kind() != reflect.String {
				continue
			}
			if key.String() != columnName && key.String() != conventionField {
				continue
			}
			return stringValue(iter.Value())
		}
	case reflect.Struct:
		if st.Schema != nil {
			if field := st.Schema.LookUpField(conventionField); field != nil {
				return writtenTenant(st.Context, field, rv)
			}
		}
	}
	return "", false
}

// writtenTenant reads the tenant column as the caller left it, reporting
// whether the caller wrote anything there.
//
// Field.ValueOf's second result is whether the field holds its zero value, not
// whether the read succeeded — reading it the other way round makes a field
// someone filled in look unwritten and a field nobody touched look written,
// which is the exact inversion this check exists to catch.
func writtenTenant(ctx context.Context, field *gormschema.Field, dest reflect.Value) (string, bool) {
	v, isZero := field.ValueOf(ctx, dest)
	if isZero || v == nil {
		return "", false
	}
	return stringValue(reflect.ValueOf(v))
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

// destinations lists the values an insert will read the row from: the struct a
// pointer points at, or each element of a slice, which is how a batch insert
// arrives.
func destinations(dest any) []reflect.Value {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil
	}
	rv = rv.Elem()
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]reflect.Value, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, rv.Index(i))
		}
		return out
	case reflect.Struct:
		return []reflect.Value{rv}
	default:
		return nil
	}
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
