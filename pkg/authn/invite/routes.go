package invite

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// mountRoutes registers the module's whole route surface from the declarations
// that also generate the fragment, so the route and its contract are one
// source. The mounter puts every response under Cache-Control: no-store and
// every unsafe method behind the cross-origin check.
//
// The management routes are registered guarded and never impersonable: the
// gateway answers an impersonating identity with a 401, and the handler checks
// the same condition again. The acceptance routes are registered public: the
// token is the whole credential and there is no identity to read.
func (p *product) mountRoutes() {
	g := func(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		p.mounter.Guarded(method, rel, h, append(opts, mount.InScope(speedhttp.ScopeTenants))...)
	}
	g(http.MethodPost, p.route(issueOperation.Path), issueOperation.Handle(p.handleIssue))
	g(http.MethodGet, p.route(listOperation.Path), listOperation.Handle(p.handleList))
	g(http.MethodDelete, p.route(revokeOperation.Path), revokeOperation.Handle(p.handleRevoke))

	pub := p.mounter.Public
	pub(http.MethodGet, p.route(acceptViewOperation.Path), acceptViewOperation.Handle(p.handleAcceptView))
	pub(http.MethodPost, p.route(acceptOperation.Path), acceptOperation.Handle(p.handleAccept))
}

// route turns a route the declaration writes against the module's API root into
// the relative path the mounter joins to its own base. The declaration is fixed
// before any configuration exists, so the path is derived from the default base
// rather than the configured one: a configured base moves the routes without
// moving the contract. A declared route that does not sit under that base is a
// declaration defect, not something to mount at a guessed path.
func (p *product) route(declared string) string {
	spec, scope := inviteSpec, speedhttp.Scope("")
	if strings.HasPrefix(declared, "/:tenant/") {
		spec, scope = inviteTenantsSpec, speedhttp.ScopeTenants
	}
	rel, err := mount.RelativePath(mount.DefaultBaseFor(scope), spec.ResolvePath(declared))
	if err != nil {
		panic(ModuleName + ": the declared route " + declared + " cannot be mounted: " + err.Error())
	}
	return rel
}
