package tenant

import "github.com/google/uuid"

const organizationPrefix = "org-"

// NewOrganizationID mints a fresh internal identifier for a tenant a client
// asked to create.
func NewOrganizationID() string {
	return organizationPrefix + uuid.NewString()
}
