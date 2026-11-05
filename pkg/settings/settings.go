// Package settings carries the parameters that change at runtime: a module
// declares the setting items it reads, an operator writes values per tenant or
// globally while the process runs, and a consumer reads them back out of an
// in-memory snapshot under the declared type.
//
// The line between configuration and settings is drawn by one judgement: a
// value belongs to configuration when it is needed before a database is
// reachable (ADR-config-scope). This module carries everything on
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
	"reflect"
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
// empty, or carrying a blank or a control character — and the global layer is
// named by its own constructor.
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
// non-empty, and free of blanks and control characters anywhere in it — so an
// id failing it can only be a caller that dropped or mangled one, and it names
// no tenant: the row it would write is one no tenant's read ever reaches, while
// a read of it falls through to the shared layer and answers with a value the
// caller never asked for.
func ForTenant(id string) Layer {
	if id == "" || strings.IndexFunc(id, notInAnIdentifier) >= 0 {
		panic("settings: ForTenant was given an identifier outside the tenant identifier's " +
			"domain, which is non-empty and free of blanks and control characters. Name the " +
			"shared layer with Global(), or fix the identifier at the call site")
	}
	return layer{tenant: id}
}

// notInAnIdentifier reports the characters no tenant identifier carries. It is
// the tenant directory's criterion, restated here: an identifier reaches log
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
	// decode under the declared type is ErrTypeMismatch, naming the key; one
	// that decodes and lies outside the item's declared domain is handed out
	// as it is, since the domain is judged when a value is written. A read
	// before this module's Init has loaded the snapshot is ErrNotLoaded rather
	// than the declared default: a module that reads in its own Init declares
	// Requires (*Store)(nil) so that it is initialised after this one. A
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
	// publishes, in that order. Validating holds the value to the declared
	// type (ErrTypeMismatch), then to the item's declared domain and to the
	// size limit (ErrInvalidValue, naming the key and the rule it breaks). A
	// failure before the row is written returns without touching anything; a
	// failure to reload or to publish does not roll the write back and does
	// not change the result.
	//
	// ctx bounds the wait for a connection and the statement. Once the row is
	// written, the reload and the publish run whatever becomes of ctx, since
	// they are this module's bookkeeping for a row already in the table: a
	// caller that gives up at that point still leaves its value served here
	// and announced to the other replicas.
	//
	// The write runs on a connection of its own from the delivered pool, never
	// in a transaction the caller holds, and waits five seconds at most for
	// that connection. Inside a transaction the caller holds on a database
	// held to one connection, such as an in-memory SQLite one, the connection
	// is the caller's, so the write fails there instead of waiting for it.
	// Elsewhere it stays outside that transaction: rolling the transaction
	// back does not undo a write that returned nil.
	//
	// The result is what happened to the row. An error means it was not
	// changed, whatever point ctx was cancelled at; nil means the change is
	// committed and the reload and the publish have run. Either of those
	// failing after the commit is logged rather than returned, and a failed
	// reload leaves this replica serving the value from before until its next
	// one. A connection lost while the change commits is the one outcome only
	// the database can settle.
	Set(ctx context.Context, w Write) error

	// Delete removes the explicit row the selector names. Deleting a row that
	// is not there is not an error. ctx bounds the wait for a connection and
	// the statement and nothing after it, the statement runs on a connection
	// of its own, and the result is what happened to the row, all as for Set.
	//
	// Delete is the one operation that does not consult the key table: it is
	// the cleanup path for rows left behind after a declaration is removed,
	// and both Get and Set refuse an undeclared key before reaching the table.
	Delete(ctx context.Context, layer Layer, key, actor string) error

	// List lists the explicit rows of the layer the selector names. The result
	// is ordered by the keys' bytes, whatever collation the database orders
	// text under, so it is the same order on every dialect.
	//
	// It hands out the written rows rather than the values reads resolve to:
	// nothing falls back here. It reads on a connection of its own, waited for
	// as Set waits for its.
	List(ctx context.Context, layer Layer) ([]Entry, error)

	// Declared lists every declared setting item, ordered by the keys' bytes
	// as List is: its key, the module that declared it, its declared type,
	// its default and the metadata its declaration gave, the rule of its
	// domain among it. It is what lets a surface over the settings — an
	// administrative endpoint, a command that prints them — name every item,
	// its type, its default and the values it takes without restating another
	// module's declaration.
	//
	// It reads the key table, which is built before Init and never changes,
	// so it takes no context, starts no IO and answers before the snapshot
	// is loaded. The result is the caller's own to modify.
	//
	// A sensitive item's default is withheld, so nothing in the result is a
	// value a sensitive item holds and it can reach a client as it is. Get
	// and List still hand out a sensitive item's value; a surface built on
	// them masks it by the Sensitive the listing reports.
	Declared() []DeclaredItem
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
	// Default is the declared default in the declared type's own text form:
	// the text a row holding it would carry, which Set accepts back and which
	// List reports for such a row. A duration's default reads "1m0s" and a
	// time's reads as its MarshalText writes it. It is empty for a sensitive
	// item, whatever the declaration holds.
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
	// TenantFacing declares that this schema states which of its items a tenant
	// surface exposes. The zero value is false: a schema belongs to the
	// platform alone unless it says otherwise, and an item that carries a
	// tenant access under a schema that does not say so is a declaration
	// defect.
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

// TenantAccess says how much of a setting item a tenant surface exposes. The
// set is closed, and the zero value is TenantNone: an item belongs to the
// platform alone unless its declaration says otherwise. A schema must also set
// Schema.TenantFacing for any item to carry an access other than TenantNone, so
// a declaration that states a tenant access without the schema-level switch
// fails the startup instead of exposing the item by accident.
type TenantAccess string

const (
	// TenantNone keeps the item on the platform surface alone. It is the zero
	// value.
	TenantNone TenantAccess = ""
	// TenantRead exposes the item to a tenant for reading. The platform alone
	// writes it. The value is part of the closed set; no item declares it yet.
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

// Domain is the value domain a declaring module gives one of its items: a
// predicate over values of the declared type, and the rule the predicate
// states, in words.
//
// It is judged when a value is written and nowhere else. Set holds the value
// to the declared type first, then to the domain, and refuses a value outside
// it with ErrInvalidValue naming the key and the rule, before anything is
// written. A read hands out what a row holds, whether or not that lies in the
// domain: a row stored before the domain was declared, or written around this
// module, is served as it is, so a consumer that cannot act on a value outside
// the domain still judges what it reads.
//
// The declaration is held to its domain when the key table is built: a domain
// over a type other than the item's declared type, one with no rule or no
// predicate, and a declared default the domain does not admit are all
// declaration defects, ErrInvalidDeclaration. The last is one because a
// default is a value a write has to be able to store back.
//
// The zero value is the domain of every value. A Domain is comparable, and so
// is an Item carrying one: two Domains are equal when they are the zero value
// or come from the same call to DomainOf.
type Domain struct {
	judge *domainJudge
}

// domainJudge is what DomainOf records: the type its predicate takes, the
// predicate over a value of that type, and the rule the refusal names.
type domainJudge struct {
	typ    reflect.Type
	admits func(value any) bool
	rule   string
}

// DomainOf builds the domain of an item declared as T: the values admits
// reports true for. rule states that set in words, as a noun phrase the
// refusal of a value outside it names — "the positive integers", "the
// durations from one second to one hour" — and Declared reports.
//
// admits receives a value of exactly the declared type, after the write has
// been held to it, and is called on the writer's goroutine, on several at once
// when writes run concurrently: it is to be a function of its argument alone,
// since the same value is judged the same way on every replica. It is called
// once more, on the declared default, when the key table is built.
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

// Rule is the domain's rule, as DomainOf was given it. It is empty for the
// zero Domain, which admits every value of the declared type.
func (d Domain) Rule() string {
	if d.judge == nil {
		return ""
	}
	return d.judge.rule
}

// Change is the payload of a change event. The tenant the change applies to
// travels on the event envelope's TenantID, empty for the global row, so the
// payload does not repeat it.
//
// Old and New are the explicit values of the layer the write acted on, not the
// values reads fall back to. For a Set, Old comes from the snapshot of the
// replica that initiated the write, which may trail the database. For a
// delete it is the text the removed row held, read back by the statement that
// removed it; a delete that removed no row publishes no change at all.
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
