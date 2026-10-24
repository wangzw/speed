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

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"unicode"
)

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

// Value implements driver.Valuer, and refuses to bind an identifier outside
// the domain: the empty one above all, which is what a scoped row's tenant
// column holds when nothing filled it.
//
// This is the one guard the convention carries with it. The filter fills the
// column where it is installed, but a command that runs a scoped model without
// the roster module has no filter at all, and a row crossing tenants is filled
// by nobody; in both cases the column would otherwise reach the database as an
// empty string, which the NOT NULL a migration declares does not stop. Refusing
// here turns that into a failed statement wherever the value is bound — an
// insert, and a condition naming the tenant by value — on whatever handle it
// runs, and with nothing beyond the standard library.
//
// The string is returned alongside the error rather than instead of it. A
// caller that reads the value and drops the error — GORM keys rows by their
// primary values that way, and a nil there panics — still gets the value it
// asked for, while every driver that binds it gets the refusal.
func (id ID) Value() (driver.Value, error) {
	switch {
	case id == "":
		return string(id), fmt.Errorf("%w: an empty tenant identifier is being bound into a statement",
			ErrNoTenantInContext)
	case !IsIdentifier(string(id)):
		return string(id), fmt.Errorf("%w: a tenant identifier carrying a blank or a "+
			"control character is being bound into a statement", ErrInvalidTenant)
	}
	return string(id), nil
}

// IsIdentifier reports whether s is a value the identifier's domain admits:
// non-empty, and free of blanks and control characters anywhere in it.
//
// The domain is part of this package's contract face. A module that layers a
// value by an identifier restates the rule rather than importing it, because
// the dependency between that module and this one is deliberately absent.
func IsIdentifier(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

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
	// StatusDeleted is a tenant that has been deleted. The row, the identifier
	// and the alias stay; the tenant is not served, and no write path changes
	// its status again. A row in this state is a tombstone.
	//
	// It is a state of its own rather than a kind of disabled because the two
	// are disposed of differently: a disabled tenant may be served again and a
	// path can wait it out, while a deleted one is terminal and the work that
	// named it is dropped. Only Delete reaches this state.
	StatusDeleted
)

// String names the state for diagnostics and for the management surface's view.
func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusDeleted:
		return "deleted"
	default:
		// Every other value, including ones written by a later build, is
		// refused rather than served.
		return "disabled"
	}
}

// DisabledBy says who disabled a tenant. The set of values is closed.
type DisabledBy int

const (
	// DisabledByOperator is the zero value: an operator disabled the tenant
	// through SetStatus, or the source was never recorded. It also covers a
	// source column holding a value this build does not recognise, so that a
	// disable nobody can account for is never revoked by an account's recovery.
	DisabledByOperator DisabledBy = iota
	// DisabledByAccount is a disable that account state caused. It is written
	// only through SetStatusByAccount, and setting the account back to enabled
	// revokes it.
	DisabledByAccount
)

// String names the source for diagnostics and for the management surface's view.
// Only DisabledByAccount reads as the account's; everything else, a value this
// build does not know included, reads as the operator's.
func (d DisabledBy) String() string {
	if d == DisabledByAccount {
		return "account"
	}
	return "operator"
}

// Tenant is one row of the roster.
type Tenant struct {
	// ID is the immutable identifier.
	ID ID
	// Alias is the name the request surface carries. It is unique across
	// the roster and may be changed. Nothing is decided by it: resolution
	// replaces it with the identifier before anything reads a tenant.
	Alias string
	// Status says whether this tenant is served, and whether it has been
	// deleted.
	Status Status
	// DisabledBy says who disabled this tenant. It is read only while Status
	// is StatusDisabled, and enabling a tenant clears it.
	DisabledBy DisabledBy
}
