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

// callbackName identifies the callbacks this plugin registers, in GORM's own
// registry.
const callbackName = "tenant:scope"

// conventionField is the Go field tenant.Model contributes. GORM flattens an
// embedded struct, so the field appears among the model's own fields under this
// Go name, on whatever column the embedding gives it.
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
// run. Everything this plugin judges comes from the statement's model — the
// schema GORM has already parsed and the model's type — and from the
// statement's context, and it keeps none of them.
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
//
// A read goes through one of two processors. Find, First, Count and Pluck run
// the query processor; Scan, Row and Rows run the row processor, and a filter
// registered on the query processor alone leaves every one of them reading
// across tenants with no tenant in the context and no error.
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
	// refused is a scoped statement whose context carries no tenant, or a
	// statement through a model that embeds the convention where no one
	// tenant column can be taken from it. The refusal comes back beside it,
	// for the caller to put on the statement in the form that statement can
	// report.
	refused
	// tenanted is a scoped statement whose context carries a tenant.
	tenanted
)

// reached is what a read reaches beyond its own model: the association joins
// reaching a scoped model, and the joins of its FROM clause into a table
// carrying tenants. A write reaches nothing beyond its model.
type reached struct {
	joins []scopedJoin
	from  []fromJoin
}

// any reports whether the read reaches anything scoped beyond its model.
func (r reached) any() bool { return len(r.joins) > 0 || len(r.from) > 0 }

// release takes this plugin's conditions back out of every join of a read that
// is no longer to be filtered.
func (r reached) release(st *gorm.Statement) {
	releaseJoins(st, r.joins)
	releaseFromJoins(st)
}

// scopeOf settles whether this statement is this plugin's business, given the
// field its model binds the tenant column to (tenantFieldOf) or the refusal of
// a model no one tenant column can be taken from, and what a read reaches
// beyond its model. It returns the tenant the statement belongs to when it has
// one, or the refusal when it has none.
//
// The answers are ordered, and the order is load-bearing. Recognising the model
// comes first, so a statement that is not scoped is left alone whatever its
// context says, and one through a model no tenant column can be taken from is
// refused whatever its context says. The cross-tenant mark comes next, so a
// statement that means to cross is not refused for lacking the very thing it
// means to lack. Only then is the identifier looked for.
//
// A statement that is scoped but not to be filtered gives up any condition an
// earlier execution of it left behind: a chain first run under one tenant and
// then carried to a crossing context, or to none, is judged by the context it
// runs under now.
//
// A read of a model that is not scoped is still scoped when one of its joins
// reaches a scoped model or a table carrying tenants: it reads that table's
// rows. The field is nil for such a read, which carries no condition of its
// own.
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

// scopeRead adds the tenant condition to a read the query processor runs, and
// to every join of it that reaches a scoped model.
func (f *filter) scopeRead(tx *gorm.DB) { f.read(tx, false) }

// scopeRow is scopeRead for the row processor, which Row, Rows and Scan run.
func (f *filter) scopeRow(tx *gorm.DB) { f.read(tx, handsBackOneRow(tx)) }

// handsBackOneRow reports whether a statement on the row processor is a Row.
// GORM's own row step tells Row from Rows and Scan by the setting they leave on
// the statement: false for the one, true for the others.
func handsBackOneRow(tx *gorm.DB) bool {
	setting, ok := tx.Get("rows")
	many, isBool := setting.(bool)
	return ok && isBool && !many
}

// read scopes one read, or refuses it in the form it can report. A read
// joined to a model or a table no tenant column can be taken from is refused as
// a read of that model would be.
//
// The tenant half of a key the read's conditions address rows by is filled
// from the context, in its WHERE and in the ON of a join of its FROM clause
// (keyHalves). A half naming another tenant is left as it stands: the read then
// reaches nothing, as a read conditioned on another tenant does.
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

// refuseRead fails a read that has no tenant to be scoped to.
//
// Two reads cannot report a refusal put on the statement, and each is given a
// form of its own that reaches the caller.
//
// A read refused while being rendered rather than run is a subquery: GORM
// renders one into the statement it is part of and drops its error on the way,
// so the refusal would reach nobody and the subquery would render as nothing —
// an empty set on one dialect, a syntax error unrelated to the cause on the
// other. It is rendered instead as a select of one value whose binding fails
// with the refusal, so that the refusal travels with the statement that does
// run, and fails it when that statement binds its arguments.
//
// A Row has no error of its own to return: it hands back the row GORM's row
// step leaves on the statement, and that step leaves none once the statement
// has failed, so the caller's Scan would dereference nil. A refused Row fails
// the statement and leaves a row of its own there instead, one whose Scan and
// Err report the refusal (refusedRow). That row reaches no database and takes
// no connection of the pool, so the refusal is answered at once even where the
// pool has no connection to spare — a statement rendered to fail at binding
// would first wait for one, and report the caller's deadline rather than the
// refusal. A Row that has already failed for some other reason is left to
// GORM's own answer.
func refuseRead(tx *gorm.DB, refusal error, oneRow bool) {
	st := tx.Statement
	switch {
	case oneRow && tx.Error == nil && !tx.DryRun:
		_ = tx.AddError(refusal)
		st.Dest = refusedRow(refusal)
	case tx.DryRun && st.SQL.Len() == 0:
		// The return value is the statement's accumulated error, which the
		// statement now holds; the subquery carries all of it.
		_ = tx.AddError(refusal)
		renderRefusal(st, tx.Error)
	default:
		_ = tx.AddError(refusal)
	}
}

// renderRefusal writes a refused subquery as a select of one value that refuses
// to be bound.
func renderRefusal(st *gorm.Statement, err error) {
	st.SQL.WriteString("SELECT ")
	st.AddVar(&st.SQL, refusal{err: err})
}

// refusal is a value whose binding fails with the error it carries.
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

// refusingConnector answers every connection it is asked for with one error.
type refusingConnector struct{ err error }

// Connect refuses the connection with the connector's error.
func (c refusingConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }

// Driver is the connector's driver, which opens nothing either.
func (c refusingConnector) Driver() driver.Driver { return refusingDriver(c) }

// refusingDriver is the driver behind refusingConnector.
type refusingDriver struct{ err error }

// Open refuses the connection with the driver's error.
func (d refusingDriver) Open(string) (driver.Conn, error) { return nil, d.err }

// scopedJoin is one association join of a read that reaches a scoped model,
// with what its ON clause needs to render the condition at each level of the
// join.
//
// A nested join renders one ON clause per level from the same join entry, and
// the levels need not all be scoped: a purchase joined to its customer and on to
// a global country is two levels, one of each. The condition is rendered per
// level, keyed by the alias GORM gives that level's table; a level whose model
// is not scoped renders a condition that holds for every row. An alias this
// plugin did not foresee renders the tenant condition, so that a table the
// plugin did not recognise fails the statement rather than reading across
// tenants.
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

// relationChain resolves a join's name to the relationships it follows, the way
// GORM resolves it: a relationship of the model by that name, or a dotted path
// of them. A name that resolves to neither is SQL text.
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

// joinTerm is the tenant condition this plugin puts into a join's ON clause.
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

// applyJoins puts the tenant condition into the ON clause of every scoped join,
// replacing the one an earlier execution left there.
func applyJoins(st *gorm.Statement, joins []scopedJoin, id string) {
	for _, sj := range joins {
		j := &st.Joins[sj.index]
		exprs := withoutJoinTerm(j.On)
		exprs = append(exprs, joinTerm{id: id, columns: sj.columns, unscoped: sj.unscoped, fallback: sj.fallback})
		j.On = &clause.Where{Exprs: exprs}
	}
}

// releaseJoins takes this plugin's condition back out of the joins of a read
// that is no longer to be filtered.
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

// withoutJoinTerm is a join's own ON conditions, with this plugin's taken out.
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
// whole key — gets its tenant half filled the same way, and one naming another
// tenant is refused as a struct's key naming one is.
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

// keyNaming is the refusal of a write whose conditions address it by a key
// naming another tenant.
func keyNaming(st *gorm.Statement, named string) error {
	return fmt.Errorf("%w: %s is addressed by a key naming tenant %q", tenant.ErrExplicitTenantMismatch,
		st.Schema.Name, named)
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
//
// What is rendered has the tenant half of every key in the caller's conditions
// on the statement's own tenant column filled from the context (keyHalves),
// and the term keeps the list as written, so a later execution fills it again
// from its own context. The tenants other than the context's such a half names
// are returned, for a write to refuse.
func applyCondition(st *gorm.Statement, field *gormschema.Field, id string, requireCondition bool) []string {
	written := writtenConditions(st)
	halves := keyHalves{at: ownColumn(st, field), id: id}
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
// callers read back. The parentheses are put around the expression's own
// rendering (parenthesized): handed to GORM as the value of an expression, a
// WHERE nested in the list — the form GORM's association mode writes a
// deletion's conditions in — is rendered as a clause of its own, keyword
// included.
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

// parenthesized renders an expression inside parentheses.
type parenthesized struct{ expr clause.Expression }

// Build renders the expression as it renders itself, between parentheses.
func (p parenthesized) Build(builder clause.Builder) {
	_ = builder.WriteByte('(')
	p.expr.Build(builder)
	_ = builder.WriteByte(')')
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

// inKey reports whether a field is one of the model's primary fields.
func inKey(s *gormschema.Schema, field *gormschema.Field) bool {
	for _, key := range s.PrimaryFields {
		if key == field {
			return true
		}
	}
	return false
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
// that for it. An assignment whose target cannot be read as one column is
// refused for the same reason: whether it reaches the tenant column, and with
// what, is not something the filter can answer.
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

// someTenant judges a value written across tenants: whatever tenant it names,
// it has to name one.
//
// A row whose tenant column is empty, or null, belongs to no tenant, and no
// scoped statement can reach it again. A literal outside the identifier's
// domain is no tenant either. An expression is the caller's own statement of
// what the column becomes, which crossing tenants allows. A target that cannot
// be read as one column is refused here as it is under a tenant: what crossing
// allows is an expression the caller wrote for the tenant column, and nothing
// says this one is for that column or which value lands in it.
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

// otherSpellings lists the fields an insert writes, besides the tenant column's
// own, to a column the database may read as the tenant column.
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

// leftOut is the refusal of an insert whose row GORM builds without the tenant
// column.
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
			// GORM assigns the update time in place of whatever the
			// field holds.
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

// excludedTable is the name a conflict clause reads the row it was about to
// insert under, which is how GORM writes an assignment from that row.
const excludedTable = "excluded"

// reach is where the database takes an assignment's target to be.
type reach int

const (
	// elsewhere is a target the database reads as some other column.
	elsewhere reach = iota
	// tenantColumn is a target the database may read as the tenant column.
	tenantColumn
	// unread is a target that cannot be read as one column at all. It may
	// name the tenant column among others, so it is refused rather than
	// guessed at.
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

// sameIdentifier reports whether a written name may resolve to the column: its
// surrounding blanks and one pair of quotes of any of the three kinds taken
// off, and compared without regard to case.
func sameIdentifier(written, column string) bool {
	s := strings.TrimSpace(written)
	if len(s) >= 2 {
		if closing, ok := quoteClosing(s[0]); ok && s[len(s)-1] == closing {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return strings.EqualFold(s, column)
}

// quoteClosing returns the character that closes a quoted name opened by c.
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

// columnPart reads one part of a column reference off the front of s.
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

// isNameByte reports whether a byte may stand in a bare name at this position:
// a letter, an underscore or any byte of a character outside ASCII anywhere,
// and a digit or a dollar sign after the first.
func isNameByte(c byte, first bool) bool {
	switch {
	case c == '_' || c >= 0x80 || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z'):
		return true
	case ('0' <= c && c <= '9') || c == '$':
		return !first
	}
	return false
}

// unreadTarget stands, among the values a write gives the tenant column, for
// an assignment whose target cannot be read as one column. Both judgements
// refuse it: it may be the tenant column, and what lands there cannot be told.
type unreadTarget struct{ name string }

// refusal is the error a write carrying this target is refused with.
func (u unreadTarget) refusal(st *gorm.Statement) error {
	return fmt.Errorf("%w: %s assigns to %q, which cannot be read as one column and may be its "+
		"tenant column; name the column alone", tenant.ErrExplicitTenantMismatch, st.Schema.Name, u.name)
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

// isConvention reports whether a field of the model is the convention's own.
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

// embeddedSetting is the key GORM reads the embedded setting of a field under.
const embeddedSetting = "EMBEDDED"

// taggedEmbedded reports whether a field's gorm tag carries the embedded
// setting, read as GORM reads its settings. A tag that does not mention the
// word at all, in any case, is passed over without being parsed.
func taggedEmbedded(tag string) bool {
	if !containsFold(tag, embeddedSetting) {
		return false
	}
	_, ok := gormschema.ParseTagSetting(tag, ";")[embeddedSetting]
	return ok
}

// containsFold reports whether sub occurs in s without regard to case.
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
