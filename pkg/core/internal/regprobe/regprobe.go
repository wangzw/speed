// Package regprobe registers modules and supplies products on a registry
// from a package path other than the test's own, so a test can prove that a
// conflict panic names both sites. The available set is decided by imports,
// so the other party to a collision may live in a dependency rather than in
// the host's own code, which is exactly the case this package stands in for.
package regprobe

import "github.com/vislake/speed/pkg/core"

// Register registers a module, making this package the registration site.
func Register(reg *core.Registry, m core.Module) {
	reg.Register(m)
}

// Supply hands a product in, making this package the supply site.
func Supply(reg *core.Registry, t core.Token, instance any) {
	reg.Supply(t, instance)
}
