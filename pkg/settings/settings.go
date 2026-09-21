// Package settings carries the parameters that change at runtime: a module
// declares the setting items it reads, an operator writes values per tenant or
// globally while the process runs, and a consumer reads them back out of an
// in-memory snapshot under the declared type.
//
// The line between configuration and settings is drawn by one judgement: a
// value belongs to configuration when it is needed before a database is
// reachable (ADR-config-scope-2026-09-13). This module carries everything on
// the other side of that line — rate-limit thresholds, feature switches,
// notification templates.
//
// The root package is both the interface package and the implementation: it
// defines the types, registers one module and delivers the settings surface
// exclusively. Storage is the database alone, so no implementation subpackage
// exists and no extension point does either — a second backend or a second
// value type is a change to this package, not a registration.
//
// The declaration surface is the same shape configuration uses, down to the
// field-name rules, so a parameter moving from configuration to settings keeps
// its key and its carrier struct.
package settings

import (
	"context"
	"time"
)

// ModuleName is this module's name in the registry. It is also the namespace
// this module's own input items are mounted under, which is what a registering
// module's namespace always is.
const ModuleName = "settings"

// ConfigNamespace is where this module's input items live in the config data.
const ConfigNamespace = ModuleName

// ChangedEventType is the type name of the change event published after every
// successful write. It is agreed between publisher and subscriber with nothing
// checking the two ends against each other.
const ChangedEventType = "settings.changed"

// Store is the settings read and write surface. Its capability token is
// (*Store)(nil).
type Store interface {
	// Get decodes the value of key into target, under the declared type.
	//
	// The value falls back tenant row, global row, declared default; an empty
	// tenant reads the global layer, which is where the fallback starts for it.
	//
	// An undeclared key is ErrUnknownKey. A value already written that does not
	// decode under the declared type is ErrTypeMismatch, naming the key. A
	// target that is not a non-nil pointer panics: that is a mistake at the
	// call site, not a fact about the data.
	//
	// It takes no context because it reads the in-memory snapshot and starts no
	// IO, so there is nothing to cancel.
	Get(tenant, key string, target any) error

	// Set encodes w.Value under the declared type and writes it. An empty
	// w.Tenant writes the global row. A successful write publishes a
	// settings.changed event.
	//
	// It validates, writes the row, reloads this replica's snapshot and
	// publishes, in that order. A failure before the row is written returns
	// without touching anything; a failure to publish does not roll the write
	// back and does not change the result.
	Set(ctx context.Context, w Write) error

	// Delete removes one explicitly written row. Deleting a row that is not
	// there is not an error. Which layer the row belongs to is given by tenant;
	// an empty tenant names the global row.
	//
	// Delete is the one operation that does not consult the key table: it is
	// the cleanup path for rows left behind after a declaration is removed,
	// and both Get and Set refuse an undeclared key before reaching the table.
	Delete(ctx context.Context, tenant, key, actor string) error

	// List lists the explicit rows of tenant, or the global rows for an empty
	// tenant. The result is ordered by key.
	//
	// It hands out the written rows rather than the values reads resolve to:
	// nothing falls back here.
	List(ctx context.Context, tenant string) ([]Entry, error)
}

// Get is the generic wrapper over Store.Get, folding the assertion into the
// function the way core.Resolve does. A type parameter that does not match the
// declared type is ErrTypeMismatch.
func Get[T any](s Store, tenant, key string) (T, error) {
	var target T
	if err := s.Get(tenant, key, &target); err != nil {
		return target, err
	}
	return target, nil
}

// Write is one value to write. It is what Set takes.
type Write struct {
	// Tenant is the tenant the row belongs to. An empty value writes the
	// global row.
	Tenant string
	// Key is the full path of a declared setting item.
	Key string
	// Value is encoded under the declared type.
	Value any
	// Actor is recorded in the event payload. This module does not interpret
	// it.
	Actor string
}

// Entry is one explicitly written row, as List reports it. Value is the text
// as stored, not a value decoded under a declaration.
type Entry struct {
	Tenant    string
	Key       string
	Value     string
	Version   uint64
	UpdatedAt time.Time
}

// Schema is the resource type a module declares to state its setting items.
// The module puts it in its descriptor's Resources, and this module collects
// every one of them when it builds the key table.
type Schema struct {
	// Namespace is the namespace of the keys. An empty namespace puts them at
	// the top level.
	Namespace string
	// Mounts are the carrier structs. A struct's field values are the declared
	// defaults.
	Mounts []Mount
	// Items carries per-item metadata, keyed by the path relative to the
	// namespace. An item absent from here takes the defaults.
	Items map[string]Item
}

// Mount is one carrier struct, together with the path prefix it hangs from.
// The struct is a prototype: it is read for its structure and for the
// defaults, and never receives a value.
type Mount struct {
	// Path is the prefix relative to the namespace. An empty path mounts the
	// struct at the namespace root.
	Path string
	// Value is the carrier struct, or a pointer to one.
	Value any
}

// Item is the metadata of one setting item.
type Item struct {
	// Description explains the item.
	Description string
	// Sensitive keeps the value out of change events: an event for a sensitive
	// item carries neither the old nor the new value.
	Sensitive bool
}

// Change is the payload of a change event. The tenant the change applies to
// travels on the event envelope's TenantID, empty for the global row, so the
// payload does not repeat it.
//
// Old and New are the explicit values of the layer the write acted on, not the
// values reads fall back to. Old comes from the snapshot of the replica that
// initiated the write, which may trail the database.
type Change struct {
	// Key is the full path of the setting item.
	Key string
	// Old is the explicit value of that layer before the write, empty when the
	// layer held no row.
	Old string
	// New is the explicit value of that layer after the write, empty on a
	// delete.
	New string
	// Deleted reports that this was a delete, in which case New is empty.
	Deleted bool
	// Actor is the value the writer supplied. This module does not interpret
	// it.
	Actor string
	// Version is the version after the write, and 0 on a delete.
	Version uint64
	// Sensitive reports that the value was withheld: when it is set, Old and
	// New are both empty.
	Sensitive bool
}
