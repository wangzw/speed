package profile

import (
	"net/http"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// The profile routes, relative to the base path B the mounter joins them to.
// Both are addressed without an account identifier: the account is the one the
// request's identity names.
const (
	pathMe = "/me"
)

// mountRoutes registers the profile routes under the base path. GET /me is the
// one route an impersonating identity may reach; PATCH /me is not, and the
// handler checks the same condition again as the fallback for a registration
// made wrongly. Both are registered guarded, so the gateway authenticates the
// endpoint and a request without a credential never reaches a handler.
func (p *product) mountRoutes() {
	p.mounter.Guarded(http.MethodGet, pathMe, p.handleGetMe, mount.Impersonable())
	p.mounter.Guarded(http.MethodPatch, pathMe, p.handlePatchMe)
}
