package tenant

// Model is the convention a scoped model embeds: it declares that the table's
// rows belong to a tenant and supplies the tenant column, not null and part of
// the primary key. A model that embeds it is filtered by tenant; one that does
// not is left alone. The model's own key is marked primaryKey beside it, and the
// table's migration must declare the same key.
type Model struct {
	TenantID ID `gorm:"column:tenant_id;not null;primaryKey"`
}
