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
// embeds this is scoped; a model that does not is untouched by this capability
// directory, which is also what keeps a single-tenant host from carrying any of
// this. The other direction is not checkable: a model that should be scoped and
// does not embed this reads exactly like a table that is global on purpose, and
// nothing says which it is.
//
// Embedding is whatever GORM flattens into the model: this type embedded
// directly, through an exported base struct the model embeds, by pointer, or in
// a field tagged embedded. GORM does not read an embedded struct of an
// unexported type, so a base struct that is not exported carries no column onto
// the model, and the model is a global table.
//
// A model that declares a field of its own on this column — one named like this
// type's field, one of another name tagged with the column, or one another
// embedding reaches first — takes the column over. GORM binds a column to one
// field, the one reached by the shortest path and the first declared among
// equals, and reads and writes that field alone. The model is still scoped, and
// the filter fills and judges the field that took the column over. What this
// type's field brings with it is then that field's to declare: the tenant half
// of the key only when it is tagged primaryKey, and the refusal to be bound
// empty only when its type is ID.
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
// key this type does. Column order, type and the NOT NULL are the rest of it.
// The NOT NULL stops a null and not an empty string, which is what an unfilled
// column holds: where no filter fills it — a command that runs a scoped model
// without the roster module is one — the insert fails because ID refuses to be
// bound empty, not because of the constraint. Every index on a scoped table
// should also lead with the tenant column, or a single tenant's query scans the
// whole table; the key this type declares does that part on its own.
//
// A model that embeds this only to read, by tenant, a table another module
// declares and writes — the audit table is one — carries none of those
// obligations: the key led by the tenant column and the NOT NULL bind the side
// that creates the table and writes it, and what they keep out is a write. The
// filter puts the tenant condition on every read through such a model, so a
// row whose tenant column is null belongs to no tenant and no tenant's read
// reaches it. A read across tenants does, and reads that column into this
// type's field as the empty identifier with no error; a reader that has to tell
// those rows apart reads them into a tenant field that can hold a null — on a
// type that does not embed this one, or on the read-only model itself, as a
// pointer field of its own that takes the column over, which a tenant's read
// still scopes. Such a model issues
// reads only. A write through it is filled and checked like any other, but the
// table's key does not lead with the tenant column, so an upsert by its key can
// reach another tenant's row, and the table is its declaring module's to write.
// The filter cannot tell a read-only model from a scoped one; review holds this.
type Model struct {
	TenantID ID `gorm:"column:tenant_id;not null;primaryKey"`
}
