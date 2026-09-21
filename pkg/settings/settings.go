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
	"fmt"
	"strings"
	"time"
	"unicode"
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

// Layer names the layer a read or write acts on: the global layer, or one
// tenant's layer.
//
// The zero value is not a layer. A Layer comes from Global or ForTenant and
// from nowhere else, so passing a nil one is a mistake at the call site:
// nothing has a nil layer on purpose, it involves no second party and no data,
// and the first time the line runs settles it. It therefore panics rather than
// returning an error, the same judgement an illegal target for a read gets.
//
// The interface carries no accessor. The two constructors are the whole of its
// surface, and the conversion to the tenant column a statement binds is this
// package's own business (tenantOf).
type Layer interface {
	// isLayer is unexported, which closes the set of implementations to this
	// package: a Layer is what the two constructors below hand out.
	isLayer()
}

// layer is the one implementation of Layer.
//
// Its tenant is the tenant column of the rows the layer names, empty for the
// global layer. That is the storage column's spelling of the layer, not the
// interface's: the interface has no empty value, since a tenant layer's
// constructor refuses an identifier outside the tenant identifier's domain —
// empty, longer than maxIdentifier, or carrying a blank or a control character
// — and the global layer is named by its own constructor.
type layer struct {
	tenant string
}

func (layer) isLayer() {}

// Global returns the global layer.
func Global() Layer { return layer{} }

// ForTenant returns the layer of the tenant identified by id.
//
// An id outside the tenant identifier's value domain is a mistake at the call
// site, in the same class as a nil layer: it panics at once rather than
// returning an error. That domain is the one the tenant directory enforces —
// non-empty, no longer than maxIdentifier, and free of blanks and control
// characters anywhere in it — so an
// id failing it can only be a caller that dropped or mangled one, and it names
// no tenant: the row it would write is one no tenant's read ever reaches, while
// a read of it falls through to the shared layer and answers with a value the
// caller never asked for.
func ForTenant(id string) Layer {
	if outOfDomain(id) {
		panic(fmt.Sprintf("settings: ForTenant was given an identifier outside the tenant "+
			"identifier's domain, which is non-empty, at most %d bytes long, and free of blanks "+
			"and control characters. Name the shared layer with Global(), or fix the identifier "+
			"at the call site", maxIdentifier))
	}
	return layer{tenant: id}
}

// maxIdentifier is the longest identifier the tenant directory acts on,
// counted in bytes.
const maxIdentifier = 36

// outOfDomain reports whether an identifier falls outside the tenant
// identifier's value domain. The directory's criteria are three — non-empty,
// no longer than maxIdentifier, and free of blanks and control characters
// anywhere — and this states all three in one place so a caller reads the
// domain rather than assembling it.
func outOfDomain(id string) bool {
	return id == "" || len(id) > maxIdentifier || strings.IndexFunc(id, notInAnIdentifier) >= 0
}

// notInAnIdentifier reports the characters no tenant identifier carries. It is
// one of the directory's criteria, restated here: an identifier reaches log
// lines, diagnostics and error text, where a newline splits one record into
// two.
func notInAnIdentifier(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// tenantOf returns the tenant column the layer names: empty for the global
// layer, the identifier for a tenant's.
//
// A nil layer panics here, for the reason Layer gives. The assertion cannot
// fail — the unexported method leaves this package the only implementer of
// Layer, and both constructors return a layer — and it is written out anyway
// because an unchecked assertion panics with no account of what arrived.
func tenantOf(l Layer) string {
	if l == nil {
		panic("settings: a nil Layer names no layer. Name one with Global(), or with " +
			"ForTenant(id)")
	}
	named, ok := l.(layer)
	if !ok {
		panic(fmt.Sprintf("settings: a Layer of type %T is not one this package builds; layers "+
			"come from Global() and ForTenant(id)", l))
	}
	return named.tenant
}

// isGlobal reports whether the layer is the global one, the only layer the
// fallback below a tenant's does not go through.
func isGlobal(l Layer) bool { return tenantOf(l) == "" }

// describeLayer names a layer the way a failure message needs it named: the
// global layer, or the tenant identifier the layer carries. It is the layers'
// counterpart of describeTarget.
func describeLayer(l Layer) string {
	if isGlobal(l) {
		return "the global layer"
	}
	return fmt.Sprintf("tenant %q", tenantOf(l))
}

// Store is the settings read and write surface. Its capability token is
// (*Store)(nil).
type Store interface {
	// Get decodes the value of key into target, under the declared type.
	//
	// The value falls back tenant row, global row, declared default, starting
	// at the layer the selector names: naming the global layer starts the
	// fallback at the global row, which is also where it ends.
	//
	// An undeclared key is ErrUnknownKey. A value already written that does not
	// decode under the declared type is ErrTypeMismatch, naming the key. A
	// target that is not a non-nil pointer panics, and so does a layer that is
	// the zero value: both are mistakes at the call site, not facts about the
	// data.
	//
	// It takes no context because it reads the in-memory snapshot and starts no
	// IO, so there is nothing to cancel.
	Get(layer Layer, key string, target any) error

	// Set encodes w.Value under the declared type and writes it to the layer
	// w.Layer names. A successful write publishes a settings.changed event.
	//
	// It validates, writes the row, reloads this replica's snapshot and
	// publishes, in that order. A failure before the row is written returns
	// without touching anything; a failure to publish does not roll the write
	// back and does not change the result.
	Set(ctx context.Context, w Write) error

	// Delete removes the explicit row the selector names. Deleting a row that
	// is not there is not an error.
	//
	// Delete is the one operation that does not consult the key table: it is
	// the cleanup path for rows left behind after a declaration is removed,
	// and both Get and Set refuse an undeclared key before reaching the table.
	Delete(ctx context.Context, layer Layer, key, actor string) error

	// List lists the explicit rows of the layer the selector names. The result
	// is ordered by key.
	//
	// It hands out the written rows rather than the values reads resolve to:
	// nothing falls back here.
	List(ctx context.Context, layer Layer) ([]Entry, error)
}

// Get is the generic wrapper over Store.Get, folding the assertion into the
// function the way core.Resolve does. A type parameter that does not match the
// declared type is ErrTypeMismatch.
func Get[T any](s Store, layer Layer, key string) (T, error) {
	var target T
	if err := s.Get(layer, key, &target); err != nil {
		return target, err
	}
	return target, nil
}

// Write is one value to write. It is what Set takes.
type Write struct {
	// Layer names the layer the row belongs to: Global() writes the global
	// row, ForTenant(id) the tenant's. It is required, and the zero value is a
	// mistake at the call site.
	Layer Layer
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
	// Tenant is the row's tenant column, and it stays a string: it reports the
	// column as the row holds it, empty for a global row. Naming a layer to a
	// read or a write is the layer selector's job, not a string's.
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
