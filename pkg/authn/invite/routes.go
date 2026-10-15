package invite

import "net/http"

// The routes, relative to the capability's base path B. The management routes
// are addressed by the path's tenant and, for a revoke, by the invitation's
// identifier; the acceptance routes are addressed by the token and name no
// tenant.
const (
	pathAdminInvitations = "/admin/tenants/:tenant/invitations"
	pathAdminInvitation  = "/admin/tenants/:tenant/invitations/:id"
	pathInvitation       = "/invitations/:token"
)

// mountRoutes registers the module's whole route surface.
//
// The management routes are registered guarded and never impersonable: the
// gateway answers an impersonating identity with a 401, and the handler checks
// the same condition again. The acceptance routes are registered public: the
// token is the whole credential and there is no identity to read.
func (p *product) mountRoutes() {
	g := p.mounter.Guarded
	g(http.MethodPost, pathAdminInvitations, p.handleIssue)
	g(http.MethodGet, pathAdminInvitations, p.handleList)
	g(http.MethodDelete, pathAdminInvitation, p.handleRevoke)

	pub := p.mounter.Public
	pub(http.MethodGet, pathInvitation, p.handleAcceptView)
	pub(http.MethodPost, pathInvitation, p.handleAccept)
}
