package tenant

// Model is the convention a scoped model embeds: embedding it declares that the
// table's rows belong to a tenant and supplies the tenant column. The column is
// not null and part of the primary key; a model's own key is marked primaryKey
// beside it. A model that does not embed it is untouched by this capability.
//
// The migration of the declaring module must declare the same key, and every
// index on a scoped table should lead with the tenant column. A model that embeds
// it only to read another module's table carries none of these obligations and
// must issue reads only.
type Model struct {
	// TenantID is the tenant column. The primaryKey tag is load-bearing: a unique
	// constraint not led by the tenant lets an upsert reach another tenant's row.
	TenantID ID `gorm:"column:tenant_id;not null;primaryKey"`
}
