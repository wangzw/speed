package profile

import (
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func (p *product) mountRoutes() {
	mount.Declared(p.mounter, getOperation, p.mountRel(getOperation.Path),
		getOperation.Handle(p.handleGetMe), mount.Impersonable())
	mount.Declared(p.mounter, patchOperation, p.mountRel(patchOperation.Path), p.patchEntry(), mount.Impersonable())
}

func (p *product) mountRel(declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, profileSpec.ResolvePath(declared))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	return rel
}
