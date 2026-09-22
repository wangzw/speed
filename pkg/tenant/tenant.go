// Package tenant decides which tenant a request and the statements it issues
// belong to, keeps the roster of tenants that exist, and filters statements to
// the one they carry.
//
// This is the interface package. It registers no module and exports no module
// name: what registers is the resolving subpackage, which delivers Resolution
// exclusively, and the roster subpackage, which delivers Catalog exclusively.
// A model that embeds Model, and a handler that reads an identifier, import
// this package, and nothing else follows it into the assembly.
//
// Nothing here reaches past the standard library. The resolving subpackage
// takes the registry, the configuration, the HTTP entry point and the
// authentication anchor; the roster subpackage takes the database and GORM.
// Keeping those out of the interface leaves a model that embeds Model carrying
// none of them.
//
// This package does not decide what a caller may do. That is a permission
// model's work, and it is a separate capability.
package tenant

// ID is a tenant's immutable identifier.
//
// It is what the tenant column of a scoped table holds, and what the logs, the
// startup account and error text name. The name that appears on the request
// surface is an alias the roster maps to one of these, so renaming a tenant
// rewrites no row of any scoped table.
//
// An identifier is supplied by whoever creates the tenant, not generated here.
// A host migrating from an existing system carries its own numbering across,
// and a second scheme minted alongside it would sit there indefinitely.
type ID string

// Status is a tenant's state. The set of values is closed: adding one changes
// every place that branches on it, and those places live in two subpackages.
type Status int

const (
	// StatusDisabled is the zero value: a status column never written, or
	// holding a value this build does not recognise, reads as this, and the
	// tenant is not served.
	//
	// The zero value sits on the refusing side deliberately. A verdict's
	// zero value closes as a refusal for the same reason — a value nobody
	// chose must not read as permission.
	StatusDisabled Status = iota
	// StatusActive is a tenant that is served.
	StatusActive
)

// String names the state for diagnostics.
func (s Status) String() string {
	if s == StatusActive {
		return "active"
	}
	// Every other value, including ones written by a later build, is
	// refused rather than served.
	return "disabled"
}

// Tenant is one row of the roster.
type Tenant struct {
	// ID is the immutable identifier.
	ID ID
	// Alias is the name the request surface carries. It is unique across
	// the roster and may be changed. Nothing is decided by it: resolution
	// replaces it with the identifier before anything reads a tenant.
	Alias string
	// Status says whether this tenant is served.
	Status Status
}
