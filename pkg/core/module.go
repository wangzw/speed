// Package core carries the module mechanism: the module descriptor, the
// registry that holds registrations and instances, the queries modules use to
// reach each other, and the lifecycle driver that advances the fixed stages
// as far as the drive asks and then releases what it built.
//
// core interprets no resource and knows no capability. It has no third-party
// dependency, because every module imports it and its dependencies would land
// in every host's dependency list.
package core

import "context"

// Token identifies a capability in a delivery declaration, a requirement
// declaration and a lookup alike, and identifies a resource type in a resource
// query. It is written as a typed nil pointer to the designated type, for
// example (*KV)(nil).
//
// A capability token's element type must be an interface; a resource token's
// element type may be any type. An illegal token panics where it appears:
// at Register for a declaration, at the query for a lookup.
type Token = any

// Module is everything a module registers. It is immutable once registered:
// the registry builds instances, the descriptor never changes.
//
// The named fields are limited to what the registry itself interprets. Every
// other piece of outward metadata travels as a resource.
type Module struct {
	// Name identifies the module and is unique within the process. It is
	// the module's identity: a command takes modules in by name, so the
	// package that registers a module exports its name as a constant for
	// the host to write down. The registry recognises no name of its own.
	Name string
	// Requires declares construction-time dependencies on capabilities. The
	// graph they form must be acyclic.
	Requires []Requirement
	// Provides declares the capabilities this module delivers.
	Provides []Provision
	// Resources declares read-only metadata. core stores it as given and
	// never interprets it; the module that defines a resource type is the
	// one that reads it back.
	Resources []any

	// Prepare states the module's stance on its exclusive claims, judging by
	// its own configuration. The stage runs for the modules this drive
	// includes and guarantees no order within itself: no module's product
	// exists at this point, and the only thing to take up is what the host
	// supplied before the drive began. A module that cannot start — a
	// setting is missing, a premise does not hold — returns an error saying
	// what is absent; being taken in at run level, it has no way to stand
	// itself down. The stance of a call that failed is not read, so a
	// failing Prepare returns the zero Enablement with its error.
	Prepare func(ctx context.Context, reg *Registry) (Enablement, error)
	// New constructs the module's product. It runs in dependency order. A
	// module without this callback takes part in the later stages with a nil
	// instance.
	New func(ctx context.Context, reg *Registry) (any, error)
	// Migrate applies migrations, after every product is constructed and
	// before any module starts working. Dependency order.
	Migrate func(ctx context.Context, reg *Registry, instance any) error
	// Init wires modules together and publishes. Every instance exists by
	// now, so mutual references resolve here. Dependency order.
	Init func(ctx context.Context, reg *Registry, instance any) error
	// Start starts the module's own service without accepting external
	// traffic. Dependency order.
	Start func(ctx context.Context, reg *Registry, instance any) error
	// Serve begins accepting external traffic, after every Start has
	// returned. It must not block: the driver advances through the stage
	// before it enters running state, so blocking here starves every entry
	// point after it. Listen on a separate goroutine.
	//
	// Declaring Serve is what makes a module an entry point, and it places
	// the module's Stop ahead of every other module's.
	Serve func(ctx context.Context, reg *Registry, instance any) error
	// Stop stops accepting new work. The entry points, the modules that
	// declare Serve, are stopped first, in reverse dependency order among
	// themselves; every other module is stopped after the last of them
	// returns, in reverse dependency order. A request an entry point
	// admitted can reach any module in the process, so an entry point's
	// Stop returns once the work it admitted has drained, and a module that
	// declares no Serve may then refuse new work here without refusing a
	// request in flight. A failure here does not abort the stage.
	//
	// Stop may be called on an instance that was never initialised or
	// started: a startup failure rolls back every constructed instance
	// whatever stage it had reached. Tolerating that is part of the
	// contract.
	Stop func(ctx context.Context, reg *Registry, instance any) error
	// Close releases resources, once every Stop has returned. Reverse
	// dependency order, entry points included. Failures are aggregated and
	// returned together. Close carries the same never-initialised
	// obligation as Stop.
	Close func(ctx context.Context, reg *Registry, instance any) error
}

// Requirement declares a construction-time dependency on a capability.
type Requirement struct {
	// Token designates the required capability.
	Token Token
	// Optional marks the absence of a provider as a legal configuration
	// rather than a startup failure. The module then follows the default
	// behaviour its own documentation states.
	Optional bool
}

// Provision declares a capability this module delivers.
type Provision struct {
	// Token designates the delivered capability.
	Token Token
	// Exclusive claims the capability for this module alone: no other
	// provider of it may stay enabled. It is a statement about this module,
	// not about the capability, so providers need not agree; any exclusive
	// claim violated in a run fails the startup.
	//
	// It also keeps the dependency closure from pulling this provider into
	// a command: several implementations of a capability may each claim it
	// exclusively, and picking one for the host would be a decision the
	// expansion rule does not make. The host names the implementation it
	// wants on the command tree, and a capability every provider of which
	// claims exclusively has no provider in the participating set until it
	// does.
	Exclusive bool
}

// Enablement is a module's stance on its exclusive claims in Prepare, and after
// resolution the registry's verdict on it.
type Enablement struct {
	// State is the stance in Prepare, or the final verdict when read back
	// from the registry. A module states StateAuto or StateEnabled; only the
	// registry gives StateDisabled.
	State EnablementState
	// Reason says why the module is not running, and the registry fills it
	// in with the verdict. It reaches the startup diagnostics, which is the
	// only defence against a process that starts up missing a piece of
	// functionality unnoticed.
	Reason string
}

// EnablementState is the stance a module takes in Prepare, and the shape of the
// verdict the registry reaches.
type EnablementState int

const (
	// StateAuto enables the module unless it loses a resolution. It is the
	// zero value, so a module without a Prepare callback states it.
	StateAuto EnablementState = iota
	// StateEnabled enables the module explicitly.
	StateEnabled
	// StateDisabled is not a stance a module may state: one taken in at run
	// level must start, so stating this fails the startup. It is the verdict
	// resolution gives when a provider stands down over an exclusive claim,
	// and what Enablement reports for a module a drive did not take in at
	// run level.
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
// Resources are passive data and cannot report their own origin, while the
// consumer nearly always needs it: a conflict must name both modules, i18n
// assets are grouped per module, migrations are recorded per owner.
type Resource[T any] struct {
	// Module is the name of the module that declared the resource.
	Module string
	// Value is the declaration itself, stored as given.
	Value T
}
