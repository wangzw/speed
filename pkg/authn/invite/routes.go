package invite

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func (p *product) mountRoutes() {
	g := func(operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		mount.Declared(p.mounter, operation, rel, h, append(opts, mount.InScope(speedhttp.ScopeTenants), mount.Impersonable())...)
	}
	g(issueOperation, p.route(issueOperation.Path), issueOperation.Handle(p.handleIssue))
	g(listOperation, p.route(listOperation.Path), listOperation.Handle(p.handleList))
	g(revokeOperation, p.route(revokeOperation.Path), revokeOperation.Handle(p.handleRevoke))
	g(resendOperation, p.route(resendOperation.Path), resendOperation.Handle(p.handleResend))

	ordinary := func(operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		mount.Declared(p.mounter, operation, rel, h, opts...)
	}
	ordinary(acceptViewOperation, p.route(acceptViewOperation.Path), acceptViewOperation.Handle(p.handleAcceptView))
	ordinary(acceptOperation, p.route(acceptOperation.Path), acceptOperation.Handle(p.handleAccept))
	ordinary(eligibilityOperation, p.route(eligibilityOperation.Path), eligibilityOperation.Handle(p.handleEligibility))
}

// route turns a declared path into the relative path the mounter joins to its
// base. It uses the default base, so a configured base moves the routes without
// moving the contract.
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
