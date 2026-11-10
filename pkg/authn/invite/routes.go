package invite

import (
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
// The management routes are registered guarded and Impersonable: their action is
// decided in the path's tenant, so an impersonating identity is admitted and
// decided by the effective account's permissions there. The acceptance read is
// public: it only shows what a token points to. The acceptance write is
// optional: it needs a signed-in caller, and the handler refuses one without an
// identity because the token alone grants a membership no more.
func (p *product) mountRoutes() {
	g := func(operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		mount.Declared(p.mounter, operation, rel, h, append(opts, mount.InScope(speedhttp.ScopeTenants), mount.Impersonable())...)
	}
	g(issueOperation, p.route(issueOperation.Path), issueOperation.Handle(p.handleIssue))
	g(listOperation, p.route(listOperation.Path), listOperation.Handle(p.handleList))
	g(revokeOperation, p.route(revokeOperation.Path), revokeOperation.Handle(p.handleRevoke))

	pub := func(operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		mount.Declared(p.mounter, operation, rel, h, opts...)
	}
	pub(acceptViewOperation, p.route(acceptViewOperation.Path), acceptViewOperation.Handle(p.handleAcceptView))
	pub(acceptOperation, p.route(acceptOperation.Path), acceptOperation.Handle(p.handleAccept))
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
