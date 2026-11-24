// Package settings carries the parameters that change at runtime: a module
// declares the setting items it reads, an operator writes values per tenant or
// globally, and a consumer reads them back out of an in-memory snapshot.
package settings

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// ModuleName is this module's name in the registry and the namespace its own
// input items are mounted under.
const ModuleName = "settings"

// ConfigNamespace is where this module's input items live in the config data.
const ConfigNamespace = ModuleName

// ChangedEventType is the type name of the change event published after every
// successful write.
const ChangedEventType = "settings.changed"

// MaskedValue is the fixed text a management surface shows in place of a
// sensitive item's value; a write carrying it is refused.
const MaskedValue = "********"

// Layer names the layer a read or write acts on: the global layer, or one named
// by ForTenant. The zero value is not a layer, and passing one panics.
type Layer interface {
	// isLayer is unexported, which closes the set of implementations to this package.
	isLayer()
}

type layer struct {
	tenant string
}

func (layer) isLayer() {}

// Global returns the global layer.
func Global() Layer { return layer{} }

// ForTenant returns the layer of the tenant identified by id. An id outside the
// tenant identifier's domain is a mistake at the call site and panics at once.
func ForTenant(id string) Layer {
	if id == "" || strings.IndexFunc(id, notInAnIdentifier) >= 0 {
		panic("settings: ForTenant was given an identifier outside the tenant identifier's " +
			"domain, which is non-empty and free of blanks and control characters. Name the " +
			"shared layer with Global(), or fix the identifier at the call site")
	}
	return layer{tenant: id}
}

// notInAnIdentifier reports the characters no tenant identifier carries: whitespace
// and control characters, which would split a log record.
func notInAnIdentifier(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

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

func isGlobal(l Layer) bool { return tenantOf(l) == "" }

func describeLayer(l Layer) string {
	if isGlobal(l) {
		return "the global layer"
	}
	return fmt.Sprintf("tenant %q", tenantOf(l))
}

// Store is the settings read and write surface; its capability token is
// (*Store)(nil).
type Store interface {
	// Get decodes the value of key into target under the declared type, falling back
	// tenant row, global row, declared default. An undeclared key is ErrUnknownKey, a
	// bad stored value ErrTypeMismatch, a read before the load ErrNotLoaded; a bad target or layer panics.
	Get(layer Layer, key string, target any) error

	// WrittenAt reports when the key's explicit row in the layer was last written and
	// whether the layer holds one. It does not fall back; an undeclared key and a
	// read before the load are refused as for Get, and a layer or target mistake panics.
	WrittenAt(layer Layer, key string) (time.Time, bool, error)

	// Read answers, from one snapshot, what each named layer holds for a key: the
	// layer's value decoded into that read target and its own row's write time. Every
	// judgement Get makes applies; results come back in the order given.
	Read(key string, reads ...LayerRead) ([]LayerReadResult, error)

	// Set encodes w.Value under the declared type and writes it to the layer w.Layer
	// names: validate, write, reload, publish. A failure before the row is written
	// touches nothing; a reload or publish failure does not roll the write back.
	Set(ctx context.Context, w Write) error

	// Delete removes the explicit row the selector names; a missing row is not an
	// error. It does not consult the key table, and the result is what happened to
	// the row, as for Set.
	Delete(ctx context.Context, layer Layer, key, actor string) error

	// List lists the explicit rows of the named layer in the keys' byte order,
	// whatever the database collation; nothing falls back.
	List(ctx context.Context, layer Layer) ([]Entry, error)

	// Declared lists every declared setting item in key order with its module, type,
	// default and metadata. It reads the key table, so it takes no context and answers
	// before the load; a sensitive item's default is withheld.
	Declared() []DeclaredItem
}

// Get is the generic wrapper over Store.Get; a type parameter that does not match
// the declared type is ErrTypeMismatch.
func Get[T any](s Store, layer Layer, key string) (T, error) {
	var target T
	if err := s.Get(layer, key, &target); err != nil {
		return target, err
	}
	return target, nil
}

// WrittenAt is the function form of Store.WrittenAt.
func WrittenAt(s Store, layer Layer, key string) (time.Time, bool, error) {
	return s.WrittenAt(layer, key)
}

// LayerRead names one layer for Store.Read and the target that layer's value
// is decoded into.
type LayerRead struct {
	// Layer is the layer whose value and explicit row are read. It is
	// judged as Get judges a layer.
	Layer Layer
	// Target is a non-nil pointer to the key's declared type, as Get takes.
	// It receives the layer's value: the layer's own row when it holds one,
	// the declared default when it does not.
	Target any
}

// LayerReadResult is one layer's answer from Store.Read.
type LayerReadResult struct {
	// WrittenAt is when the layer's explicit row was last written. It is the
	// zero time when Present is false.
	WrittenAt time.Time
	// Present reports whether the layer held an explicit row. A value read
	// from a layer with no row is the declaration's default.
	Present bool
}

// LayerValue is one layer's answer from ReadLayers: the value in the declared
// type and the layer's explicit row.
type LayerValue[T any] struct {
	// Value is the layer's own row's value, or the declared default when the
	// layer holds no row.
	Value T
	// WrittenAt is when the layer's explicit row was last written, zero when
	// Present is false.
	WrittenAt time.Time
	// Present reports whether the layer held an explicit row.
	Present bool
}

// ReadLayers is the generic wrapper over Store.Read: one snapshot answers
// each named layer's value and explicit row, in the order given.
func ReadLayers[T any](s Store, key string, layers ...Layer) ([]LayerValue[T], error) {
	targets := make([]T, len(layers))
	reads := make([]LayerRead, len(layers))
	for i, layer := range layers {
		reads[i] = LayerRead{Layer: layer, Target: &targets[i]}
	}
	results, err := s.Read(key, reads...)
	if err != nil {
		return nil, err
	}
	values := make([]LayerValue[T], len(results))
	for i, result := range results {
		values[i] = LayerValue[T]{
			Value:     targets[i],
			WrittenAt: result.WrittenAt,
			Present:   result.Present,
		}
	}
	return values, nil
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
	Tenant string
	// Key is the item's full path.
	Key string
	// Value is the text as stored.
	Value string
	// Version is the row's write counter.
	Version uint64
	// UpdatedAt is when the row was last written.
	UpdatedAt time.Time
}

// DeclaredItem is one declared setting item, as Declared reports it.
type DeclaredItem struct {
	// Key is the full path of the item, the one Get, Set, Delete and Entry
	// name it by.
	Key string
	// Module is the name of the module that declared it.
	Module string
	// Type is the declared type. A read's target is a pointer to exactly this
	// type, so reflect.New(Type) is a target Get accepts for the key.
	Type reflect.Type
	// Default is the declared default in the declared type's own text form, the
	// text Set accepts back; empty for a sensitive item.
	Default string
	// Item is the metadata the declaration gave. Its Sensitive also says that
	// Default was withheld, and its Domain's Rule states the values a write
	// of the key may store.
	Item
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
	// TenantFacing declares that the schema states which items a tenant surface
	// exposes; an item with a tenant access under a schema that does not is a
	// declaration defect.
	TenantFacing bool
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

// TenantAccess says how much of a setting item a tenant surface exposes. It is a
// closed set and the zero value is TenantNone; the schema must also set
// TenantFacing.
type TenantAccess string

const (
	// TenantNone keeps the item on the platform surface alone. It is the zero
	// value.
	TenantNone TenantAccess = ""
	// TenantRead exposes the item to a tenant for reading; the platform alone writes
	// it.
	TenantRead TenantAccess = "read"
	// TenantReadWrite exposes the item to a tenant for reading and writing.
	TenantReadWrite TenantAccess = "read-write"
)

// Control names the control a tenant surface renders for an item. The set is
// closed, and the zero value declares no control: a tenant-facing item must
// name one.
type Control string

const (
	// ControlNone declares no control. It is the zero value.
	ControlNone Control = ""
	// ControlSwitch renders a switch for a boolean item.
	ControlSwitch Control = "switch"
)

// Danger says how destructive turning an item off is, and so whether a surface
// confirms the change first. The zero value declares no danger.
type Danger string

const (
	// DangerNone declares no danger. It is the zero value.
	DangerNone Danger = ""
	// DangerCritical marks a change a surface confirms before saving.
	DangerCritical Danger = "critical"
)

// Effect says when a change to an item takes effect. The zero value declares no
// effect.
type Effect string

const (
	// EffectNone declares no effect. It is the zero value.
	EffectNone Effect = ""
	// EffectImmediate marks a change that applies as soon as it is saved.
	EffectImmediate Effect = "immediate"
	// EffectNextSignIn marks a change that applies to a member at the next
	// sign-in, or at a comparable later event the item's declaration names.
	EffectNextSignIn Effect = "next-sign-in"
)

// Item is the metadata of one setting item.
type Item struct {
	// Description explains the item. Declared reports it. It is the text a
	// tenant surface falls back to when no localizer is present, so a
	// tenant-facing item carries one.
	Description string
	// TitleKey is the i18n key of the item's title. A tenant-facing item must
	// declare one.
	TitleKey string
	// DescriptionKey is the i18n key of the item's description. A
	// tenant-facing item must declare one.
	DescriptionKey string
	// Control is the control a tenant surface renders for the item. A
	// tenant-facing item must name one.
	Control Control
	// Danger is how destructive turning the item off is. The zero value
	// declares none.
	Danger Danger
	// Effect is when a change to the item takes effect. The zero value
	// declares none.
	Effect Effect
	// TenantAccess is how much of the item a tenant surface exposes. The zero
	// value keeps it on the platform surface alone.
	TenantAccess TenantAccess
	// Sensitive keeps the value out of change events and out of Declared: an
	// event for a sensitive item carries neither the old nor the new value,
	// and the listing carries no default for it.
	Sensitive bool
	// Domain is the set of values of the declared type a write may store.
	// The zero Domain admits every value of the type; DomainOf builds one
	// that admits fewer.
	Domain Domain
}

// Domain is the value domain a declaring module gives one of its items: a predicate
// over values of the declared type and the rule it states. It is judged at a write
// and at key-table build, never at a read; the zero value admits every value.
type Domain struct {
	judge *domainJudge
}

type domainJudge struct {
	typ    reflect.Type
	admits func(value any) bool
	rule   string
}

// DomainOf builds the domain of an item declared as T. admits receives a value of
// exactly T on the writer goroutine, possibly concurrently, so it must be a
// function of its argument alone.
func DomainOf[T any](rule string, admits func(value T) bool) Domain {
	judge := &domainJudge{typ: reflect.TypeFor[T](), rule: rule}
	if admits != nil {
		judge.admits = func(value any) bool {
			typed, ok := value.(T)
			return ok && admits(typed)
		}
	}
	return Domain{judge: judge}
}

// Rule is the domain's rule, empty for the zero Domain.
func (d Domain) Rule() string {
	if d.judge == nil {
		return ""
	}
	return d.judge.rule
}

// Change is the payload of a change event. Old and New are the explicit values of
// the layer the write acted on, not the values reads fall back to.
type Change struct {
	// Key is the full path of the setting item.
	Key string
	// Old is the explicit value of that layer before the write, empty when the
	// layer held no row. A delete always removed one, so its Old is that row's
	// text.
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
