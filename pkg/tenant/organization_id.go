package tenant

import "github.com/google/uuid"

const organizationPrefix = "org-"

// NewOrganizationID mints a fresh identifier for a tenant a client asked to
// create. The identifier is internal: no request surface accepts one and no
// view answers one.
func NewOrganizationID() string {
	return organizationPrefix + uuid.NewString()
}
