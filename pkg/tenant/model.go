package tenant

// Model is the convention a scoped model embeds. It is both the declaration
// and the column: embedding it says this table's rows belong to a tenant, and
// it is where that column comes from.
//
// Declaration and column are one act rather than two. A marker interface
// standing beside a separately written column would let the two disagree, and
// every way they can disagree surfaces at run time as the same class of
// failure — the filtering accepts the model while a statement reports no such
// column, or it declines the model and the statement reads across tenants.
// Embedding removes that class rather than detecting it.
//
// The column name is fixed here because the filter has to find it. A model that
// embeds this is scoped; a model that does not is untouched by this release
// unit, which is also what keeps a single-tenant host from carrying any of
// this. The other direction is not checkable: a model that should be scoped and
// does not embed this reads exactly like a table that is global on purpose, and
// nothing says which it is.
//
// The column is declared not null and part of the primary key, and the second
// half is load-bearing rather than tidy. A unique constraint that does not lead
// with the tenant column — an id generated globally, a business key unique on
// its own — lets one tenant's write reach another's row through the upsert a
// Save or an OnConflict produces: the insert conflicts on the other tenant's
// key and updates that row into the writer's tenant. Making the tenant column
// part of the key removes the shape from the schema, and with it the whole
// class, rather than leaving it to be checked per statement.
//
// A model's own key is marked primaryKey beside this one, giving the composite
// key a scoped table has. A model with no other key of its own would end up
// keyed by its tenant alone, which is a modelling mistake rather than something
// worth guarding against.
//
// One obligation stays with the module that declares the table, because this
// one does not read other modules' models: the migration must declare the same
// key this type does. Column order, type and the NOT NULL are the rest of it —
// without the constraint, a statement issued while the filter is not installed
// writes an empty value instead of failing. Every index on a scoped table
// should also lead with the tenant column, or a single tenant's query scans the
// whole table; the key this type declares does that part on its own.
type Model struct {
	TenantID ID `gorm:"column:tenant_id;not null;primaryKey"`
}
