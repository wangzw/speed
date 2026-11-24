// Package core carries the module mechanism: module descriptors, the registry, the capability and resource queries, and the lifecycle driver.
// It interprets no capability and no resource, and takes no third-party dependency.
package core

import "context"

// Token identifies a capability or a resource type, written as a typed nil pointer to the designated type.
// A capability token's element type must be an interface; a resource token's may be any type. An illegal token panics where it appears.
type Token = any

// Module is everything a module registers and is immutable once registered.
// Its named fields are limited to what the registry interprets; every other piece of outward metadata travels as a resource.
type Module struct {
	// Name identifies the module and is unique within the process.
	// It is the module's identity: a command takes modules in by name, so the package that registers a
	// module exports its name as a constant for the host to write down.
	Name string
	// Requires declares construction-time dependencies on capabilities.
	// The graph they form must be acyclic.
	Requires []Requirement
	// Provides declares the capabilities this module delivers.
	// The module's instance has to implement every one of them, whether its New constructed it or the
	// host supplied it, and the registry checks that it does.
	Provides []Provision
	// Resources declares read-only metadata.
	// core stores it as given and never interprets it; the module that defines a resource type is the
	// one that reads it back.
	Resources []any

	// Prepare states the module's stance from its own configuration, in no particular order.
	// It is skipped for a supplied module, whose stance is StateEnabled; a failing call's stance is not read.
	Prepare func(ctx context.Context, reg *Registry) (Enablement, error)
	// New constructs the module's product in dependency order.
	// A module without New takes part with a nil instance unless the host supplied one; New does not run for a supplied module.
	New func(ctx context.Context, reg *Registry) (any, error)
	// Migrate applies migrations after every product is constructed, in dependency order.
	// It receives the module's instance, whether New built it or the host supplied it.
	Migrate func(ctx context.Context, reg *Registry, instance any) error
	// Init wires modules together and publishes, in dependency order; every instance exists by then.
	Init func(ctx context.Context, reg *Registry, instance any) error
	// Start starts the module's own service without accepting external traffic, in dependency order.
	Start func(ctx context.Context, reg *Registry, instance any) error
	// Serve begins accepting external traffic after every Start has returned and must not block; listen on a separate goroutine.
	// Declaring Serve makes the module an entry point, whose Stop runs first.
	Serve func(ctx context.Context, reg *Registry, instance any) error
	// Stop stops accepting new work; entry points stop first in reverse dependency order, then the rest.
	// A failure does not abort the stage. It may be called on an instance never initialised or started.
	Stop func(ctx context.Context, reg *Registry, instance any) error
	// Close releases resources after every Stop has returned, in reverse dependency order, entry points included.
	// Failures are aggregated and returned together; it carries the same never-initialised obligation as Stop.
	Close func(ctx context.Context, reg *Registry, instance any) error
}

// Requirement declares a construction-time dependency on a capability.
type Requirement struct {
	// Token designates the required capability.
	Token Token
	// Optional marks the absence of a provider as a legal configuration rather than a startup failure; the module then follows its documented default.
	Optional bool
}

// Provision declares a capability this module delivers.
type Provision struct {
	// Token designates the delivered capability.
	Token Token
	// Exclusive claims the capability for this module alone: no other enabled provider may stay.
	// It also keeps the dependency closure from pulling this provider into a command; the host names the implementation it wants.
	Exclusive bool
}

// Enablement is a module's stance in Prepare, and after resolution the registry's verdict on it.
type Enablement struct {
	// State is the stance in Prepare, or the final verdict when read back from the registry.
	// A module states any of the three; the registry reaches the same three as verdicts.
	State EnablementState
	// Reason says why the module is not running.
	// A module that states StateDisabled supplies it, and it travels unchanged into the verdict; the
	// registry fills it in when resolution stands a module down.
	Reason string
}

// EnablementState is the stance a module takes in Prepare, and the shape of the verdict the
// registry reaches.
type EnablementState int

const (
	// StateAuto enables the module unless it loses a resolution.
	// It is the zero value, so a module without a Prepare callback states it.
	StateAuto EnablementState = iota
	// StateEnabled enables the module explicitly.
	// It is the stance of a module the host supplied an instance for, whose Prepare does not run to
	// state another.
	StateEnabled
	// StateDisabled stands the module down for this run, with the reason the module gives.
	// The module expects to run without the optional piece of configuration it names, so its absence
	// is not a startup failure; a setting with no usable value is reported as a Prepare error instead.
	StateDisabled
)

// String renders the state for diagnostics.
func (s EnablementState) String() string {
	switch s {
	case StateAuto:
		return "auto"
	case StateEnabled:
		return "enabled"
	case StateDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// Resource is one resource declaration together with the module that made it.
type Resource[T any] struct {
	// Module is the name of the module that declared the resource.
	Module string
	// Value is the declaration itself, stored as given.
	Value T
}
