// Package regprobe registers modules and supplies instances on a registry
// from a package path other than the test's own, so a test can prove that a
// panic names both sites. The available set is decided by imports, so the
// other party to a collision may live in a dependency rather than in the
// host's own code, which is exactly the case this package stands in for.
//
// It also defines an instance type of its own. Claiming an instance by its type
// compares the package that defines the type with the package that registered
// each candidate, so a test needs a type that is defined in a package that
// registers modules.
package regprobe

import "github.com/vislake/speed/pkg/core"

// Register registers a module, making this package the registration site.
func Register(reg *core.Registry, m core.Module) {
	reg.Register(m)
}

// Registrar registers a module from a method, which gives the registration
// site a function name of another shape than Register's.
type Registrar struct{}

// Register registers a module, making this package the registration site.
func (Registrar) Register(reg *core.Registry, m core.Module) {
	reg.Register(m)
}

// RegisterInClosure registers a module from a function literal, which gives
// the registration site a third shape of function name.
func RegisterInClosure(reg *core.Registry, m core.Module) {
	func() { reg.Register(m) }()
}

// Supply hands an instance in, making this package the supply site.
func Supply(reg *core.Registry, instance any, module ...string) {
	reg.Supply(instance, module...)
}

// Product is an instance type defined in this package. It satisfies the Cache
// and Store capabilities the black-box tests declare without naming either:
// capabilities are satisfied structurally.
type Product struct{ ID string }

// CacheID satisfies the tests' Cache capability.
func (p *Product) CacheID() string { return p.ID }

// StoreID satisfies the tests' Store capability.
func (p *Product) StoreID() string { return p.ID }
