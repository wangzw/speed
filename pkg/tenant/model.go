package tenant

// Model is the convention a scoped model embeds: declaring it supplies the tenant column, which GORM
// binds by the field's struct, not its name. The column is NOT NULL and part of the primary key, and the
// migration must declare the same key with it leading. An embedding GORM does not read is refused.
type Model struct {
	TenantID ID `gorm:"column:tenant_id;not null;primaryKey"`
}
