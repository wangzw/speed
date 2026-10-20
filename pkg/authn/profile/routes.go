package profile

import (
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// mountRoutes registers the two profile routes through the mounter. Each route
// is registered from the operation that declares it, so the handler's input and
// answer types, the fragment and the route are one source. Both are guarded, so
// the gateway authenticates the endpoint and a request without a credential
// never reaches a handler.
//
// GET /me is the one route an impersonating identity may reach, and PATCH /me is
// not: the gateway refuses an impersonating identity there, guardPatch refuses it
// again, and the typed handler refuses it a third time as the fallback for a
// registration made wrongly.
func (p *product) mountRoutes() {
	mount.Declared(p.mounter, getOperation, p.mountRel(getOperation.Path),
		getOperation.Handle(p.handleGetMe), mount.Impersonable())
	mount.Declared(p.mounter, patchOperation, p.mountRel(patchOperation.Path), p.patchEntry())
}

// mountRel turns a declared route into the relative path the mounter joins to
// its base. The declaration writes the route against the module's API root, and
// the fragment is a static declaration fixed before configuration, so the part
// of it the capability owns is the default mount base less that root. Removing
// it leaves the relative path the mounter takes, whatever base the deployment
// configured: a configured base moves the routes without moving the contract,
// which is the split the Spec already accepts. A declared route that does not
// begin with the owned part is a declaration defect, not something to mount at a
// guessed path.
func (p *product) mountRel(declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, profileSpec.ResolvePath(declared))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	return rel
}
