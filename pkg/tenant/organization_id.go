package tenant

import "github.com/google/uuid"

// organizationPrefix is the fixed prefix of every generated organization
// identifier. The rest is a random UUID, so the string carries no display name
// or other personal data and cannot be predicted from one.
const organizationPrefix = "org-"

// NewOrganizationID mints a fresh identifier for a tenant a client asked to
// create. The identifier is internal: no request surface accepts one, and no
// view answers one. It is generated here so that the self-service creation and
// the platform registration draw their identifiers from one generator.
func NewOrganizationID() string {
	return organizationPrefix + uuid.NewString()
}
