package tenant

import (
	"context"
	nethttp "net/http"
)

// Extractor reads a tenant name off the request surface.
//
// It sees the request and nothing else. The name it returns is an alias — the
// roster is what turns one into an identifier, and an extractor does not know
// the roster exists.
//
// A name that is non-empty and free of blanks is well formed; whether it names
// a tenant is the roster's to decide. An extractor that finds no name returns
// false, and the resolving layer decides what that means for the request.
//
// An extractor runs on every request of the endpoints it guards, so it must not
// block and must not read the request body: reading it once consumes it, and
// the handler's own decode then sees an empty one while the caller's request
// was complete.
type Extractor interface {
	Extract(r *nethttp.Request) (string, bool)
}

// Resolution is the capability the resolving subpackage delivers, and the
// surface an extractor is registered on.
//
// It is taken up with core.Resolve[tenant.Resolution](reg) in the registrant's
// own Init, which is the only window in which registration holds. A call
// outside that window is a mistake at the call site rather than a condition to
// report, and it panics, as the entry point's own registration surface does.
//
// Registration holds for the Init stage rather than for this module's own Init
// callback: a module registering an extractor declares no dependency on this
// one, so the two may run in either order.
type Resolution interface {
	// Register declares an extractor under a name the configuration selects
	// it by. The name is non-empty and free of blanks, and two registrations
	// under one name fail the startup.
	//
	// The registration surface does not know who is registering: it is
	// handed a name and an implementation, so a conflict names the name and
	// the two implementation types rather than the modules behind them.
	Register(name string, e Extractor)
}

// Catalog is the capability the roster subpackage delivers: the roster's read
// and write surface.
//
// Writing through it is this release unit's only operations-facing action. A
// host takes it up to register tenants and to change their state; nothing else
// in the assembly adds a tenant.
//
// None of these methods is scoped to a tenant. The roster is the fact that
// scoping is decided from, so its own statements run outside the filter, and
// none of them needs a tenant in its context.
type Catalog interface {
	// Get returns the tenant with this identifier. The second result is
	// false when the roster has no such row.
	Get(ctx context.Context, id ID) (Tenant, bool, error)
	// ByAlias returns the tenant this name belongs to, which is the lookup
	// the resolving layer makes on every request.
	ByAlias(ctx context.Context, alias string) (Tenant, bool, error)
	// Put registers a tenant, or rewrites the row its identifier already
	// has. Alias and status are taken from the value as given.
	//
	// It is idempotent by identifier. A second tenant arriving with an
	// alias another one holds is refused, and nothing is written.
	Put(ctx context.Context, t Tenant) error
	// SetStatus changes one tenant's state. Disabling goes through here and
	// only here; there is no delete.
	SetStatus(ctx context.Context, id ID, s Status) error
	// List returns every tenant the roster holds.
	List(ctx context.Context) ([]Tenant, error)
}
