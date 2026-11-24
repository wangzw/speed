// Package dotted offers regprobe's registrations from an import path whose last element contains a dot.
package dotted

import "github.com/vislake/speed/pkg/core"

// Register registers a module, making this package the registration site.
func Register(reg *core.Registry, m core.Module) {
	reg.Register(m)
}

// Registrar registers a module from a method, which gives the registration site a function name of
// another shape than Register's.
type Registrar struct{}

// Register registers a module, making this package the registration site.
func (Registrar) Register(reg *core.Registry, m core.Module) {
	reg.Register(m)
}

// RegisterInClosure registers a module from a function literal, which gives the registration site
// a third shape of function name.
func RegisterInClosure(reg *core.Registry, m core.Module) {
	func() { reg.Register(m) }()
}

// Supply hands an instance in, making this package the supply site.
func Supply(reg *core.Registry, instance any, module ...string) {
	reg.Supply(instance, module...)
}

// Product is an instance type defined in this package.
// It satisfies the Cache and Store capabilities the black-box tests declare without naming either:
// capabilities are satisfied structurally.
type Product struct {
	// ID identifies the product.
	ID string
}

// CacheID satisfies the tests' Cache capability.
func (p *Product) CacheID() string { return p.ID }

// StoreID satisfies the tests' Store capability.
func (p *Product) StoreID() string { return p.ID }
