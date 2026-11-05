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
// It is taken up with core.Resolve[tenant.Resolution](reg) from a registrant's
// own Init callback, and registration is accepted for a window that contains
// the whole Init stage: it opens in this module's Migrate callback and closes
// in its Start callback. A call outside that window is a mistake at the call
// site rather than a condition to report, and it panics, as the entry point's
// own registration surface does.
//
// The window is wider than this module's own Init callback for a reason: a
// module registering an extractor declares no dependency on this one, so the
// two may run in either order.
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

// Admission is the capability the admission subpackage delivers: the layer that
// decides whether an identified caller may enter the tenant a request resolved
// to. It is an ordering anchor and not a surface: a layer that has to sit
// inside admission, an authorization layer above all, names
// (*tenant.Admission)(nil) in its After and calls nothing on it.
//
// The marker method is exported, and that is not a style choice. The interface
// is defined here and delivered by a type in another package, and a type of
// another package cannot implement an interface whose method is unexported, so
// the delivery check would fail for the one implementation there is. A package
// importing this one for the anchor needs nothing else of it.
type Admission interface {
	// AdmissionLayer marks the implementation. It does nothing.
	AdmissionLayer()
}

// Catalog is the capability the roster subpackage delivers: the roster's read
// and write surface.
//
// Writing through it is this capability directory's only operations-facing
// action. A host takes it up to register tenants and to change their state;
// nothing else in the assembly adds a tenant.
//
// A roster that has not been read refuses these calls rather than answering as
// though it held no tenant: which tenants exist is a fact about the table, and
// only a read that has happened can state it.
//
// None of these methods is scoped to a tenant. The roster is the fact that
// scoping is decided from, so its own statements run outside the filter, and
// none of them needs a tenant in its context.
//
// A method that reads the table or writes it — Put, Create, SetStatus,
// SetStatusByAccount, Delete, SetProfile, SetOwner, and an entry the snapshot
// would refuse — runs its
// statements on a connection of the
// delivered pool of its own, not in the caller's transaction, taken through the
// database module's Connection within five seconds. No connection within them
// fails with ErrNoConnection, having done nothing: on a database held to one
// connection — an in-memory one — a caller inside its own transaction on the
// delivered handle holds that connection until its own call returns, so such a
// call made inside that transaction fails once the five seconds have passed.
//
// The statements themselves run under the caller's context, and wait for a
// lock another connection holds as the engine does, failing with the engine's
// own error when it gives up. Inside a caller's transaction on the same SQLite
// database file, a write waits for the caller's write lock for the
// connection's busy timeout and then fails. A transaction the database module
// begins other than DEFERRED — IMMEDIATE by default; one begun read-only, or
// on a locator naming _txlock=deferred, begins DEFERRED — holds the write lock
// from its BEGIN until its call returns, whether or not it has read or
// written, and the write's own transaction asks for that lock at its BEGIN. A
// caller's transaction begun DEFERRED fails a write the same way once it has
// written, and in rollback-journal mode once it has read, since the write's
// COMMIT then waits for it to finish reading. On PostgreSQL, a write to
// a row the caller's own transaction has written waits for that transaction,
// until the caller's context ends or the session's lock_timeout or
// statement_timeout does, and with none of them for good. Get, ByAlias and
// List read the snapshot and nothing else.
//
// Enter and EnterRegistered are the methods that put a tenant into a context,
// and they are here rather than beside FromContext because the roster is what
// they have to read first: a path with no request behind it names a tenant by
// its identifier, and only a tenant the roster carries is entered — by Enter
// only while the roster serves it.
type Catalog interface {
	// Get returns the tenant with this identifier. The second result is
	// false when the roster has no such row.
	Get(ctx context.Context, id ID) (Tenant, bool, error)
	// ByAlias returns the tenant this name belongs to, which is the first
	// lookup the resolving layer makes on every request; a name that is no
	// alias is then looked up as an identifier.
	ByAlias(ctx context.Context, alias string) (Tenant, bool, error)
	// Put registers a tenant, or rewrites the row its identifier already
	// has. Alias, status and disable source are taken from the value as given.
	// The owner is written on an insert and left alone on a rewrite: Put is not
	// the transfer path, SetOwner is.
	//
	// It is idempotent by identifier. A second tenant arriving with an
	// alias another one holds is refused, and nothing is written. A row that
	// is a tombstone is refused with ErrTenantDeleted, and nothing is written:
	// a deleted tenant is terminal. So is a value whose status is StatusDeleted
	// or whose disable source is anything but the operator's, refused with
	// ErrInvalidTenant — deletion goes through Delete, and an account-caused
	// disable only through SetStatusByAccount.
	Put(ctx context.Context, t Tenant) error
	// Create registers a tenant that is not registered yet, and refuses one
	// whose identifier the roster's table already holds with ErrTenantExists,
	// writing nothing — a tombstone's identifier included, so a deleted tenant
	// is never registered a second time. The value's owner, profile and the
	// moment of creation are written with the row. An alias another tenant holds
	// is refused as Put refuses it, and a value whose status is StatusDeleted or
	// whose disable source is not the operator's is refused with
	// ErrInvalidTenant, as Put refuses it.
	//
	// It is how a registration that must not overwrite is written: whether
	// the identifier is taken is asked of the table rather than of this
	// replica's snapshot, which may predate another replica's registration,
	// and two creations of one identifier at once end with one tenant and
	// one refusal. Checking with Get and then calling Put has neither
	// property: a replica whose snapshot has not seen a tenant yet rewrites
	// its alias and status — re-enabling it if it was disabled.
	Create(ctx context.Context, t Tenant) error
	// SetStatus is the operator's state transition. Disabling writes the
	// source as the operator's, even on a row already disabled — the operator
	// has restated the decision — and enabling clears it. There is no delete
	// here: s holding StatusDeleted is refused with ErrInvalidTenant, and a
	// tombstone is refused with ErrTenantDeleted.
	SetStatus(ctx context.Context, id ID, s Status) error
	// SetStatusByAccount is the state transition account state drives. It
	// disables only a tenant that is active, writing the source as the
	// account's, and enables only a tenant that is disabled by the account,
	// clearing the source. Any other request writes nothing and returns nil,
	// so repeating a call changes nothing and a no-op costs no reread. A
	// tombstone is refused with ErrTenantDeleted, and s holding StatusDeleted
	// with ErrInvalidTenant.
	SetStatusByAccount(ctx context.Context, id ID, s Status) error
	// Delete writes one tenant as deleted, the terminal state: the row, the
	// identifier, the alias, the owner and the disable source stay, status
	// reads as deleted, and the moment is recorded in the deletion column. The
	// tenant is never served again. Deleting a tombstone again writes nothing
	// and returns nil. An identifier no row carries, or one outside the domain,
	// is refused with ErrInvalidTenant.
	Delete(ctx context.Context, id ID) error
	// SetOwner writes one tenant's owner account and nothing else. It is the
	// transfer path: the alias, the status, the profile and the creation time
	// are not named, so a transfer cannot undo a disable or a profile update
	// committed in the same window. A business organization always has an owner,
	// so the empty value is refused with ErrInvalidTenant, as is a value carrying
	// a blank or a control character. Writing the owner a row already carries is
	// a no-op. An identifier no row carries is refused with ErrInvalidTenant, and
	// a tombstone with ErrTenantDeleted: its owner stays with the row.
	SetOwner(ctx context.Context, id ID, owner string) error
	// SetProfile rewrites one tenant's organization profile and nothing else:
	// the display name, the description, the contact email and the website.
	// The alias, the owner, the status, the disable source and the creation
	// time are not
	// written, so a profile update cannot undo a disable or a
	// deletion another path committed in the same window. Put is not the route
	// for this write: it takes the whole row's values, the profile included
	// only as it stands.
	//
	// A profile outside the domain this package states is refused with
	// ErrInvalidProfile and nothing is written. An identifier no row carries is
	// refused with ErrInvalidTenant, and a tombstone with ErrTenantDeleted: a
	// deleted tenant's profile is as terminal as the rest of its row.
	SetProfile(ctx context.Context, id ID, profile Profile) error
	// List returns every tenant the roster holds, ordered by identifier, as a
	// copy: a caller sorting or trimming what it was handed does not disturb
	// what the next caller sees.
	List(ctx context.Context) ([]Tenant, error)
	// Enter returns a context that belongs to this tenant, as a resolved
	// request's does: FromContext answers it, scoped statements issued under
	// it are conditioned on it, filled with it and checked against it, and
	// the logger it carries has the identifier bound as the tenant field.
	//
	// It is how a path with no request behind it — an event subscriber, a
	// worker, a command-line action — acts for one tenant, instead of crossing
	// tenants and writing the tenant into every statement by hand. The
	// identifier is read against the roster: one no row carries is refused
	// with ErrInvalidTenant, one that is disabled with ErrTenantDisabled, and
	// one that is a tombstone with ErrTenantDeleted — a deleted tenant cannot
	// be waited out.
	//
	// A refusal is the table's answer, not a snapshot's: where this replica's
	// snapshot would refuse, the tenant's row is read from the table, so a
	// tenant another replica registered or served again since this one last
	// read the roster is entered at once. A table that cannot be read at that
	// point gives neither refusal but the failure itself: ErrRosterMissing when
	// the table is not there, with the read's own error in the chain;
	// ErrNoConnection for a read that got no connection — which is what an
	// in-process subscriber entering a disabled or unknown tenant meets inside
	// its publisher's transaction on a database held to one connection; the
	// context's error when the caller's context ended; and any other failure
	// of the read — a lock the engine gave up waiting for, as its own timeouts
	// decide, and a lost connection among them — as it stands. None of them is
	// an answer about the tenant. An admission comes
	// from the snapshot and reads nothing, so a tenant disabled on another
	// replica is still entered here until this replica next rereads the
	// roster.
	//
	// The context returned carries no cross-tenant mark, and whatever tenant
	// the one passed in carried is replaced. On a refusal the context returned
	// carries neither a tenant nor the mark, so a caller that uses it anyway
	// has every scoped statement refused rather than let across.
	Enter(ctx context.Context, id ID) (context.Context, error)
	// EnterRegistered is Enter for a tenant whatever its status: it enters a
	// disabled tenant as well as a served one, and refuses only an identifier
	// no row carries, with ErrInvalidTenant.
	//
	// It is how work that belongs to one tenant is done while that tenant is
	// not served — reading its trail, exporting its data, deleting it — with
	// the filter scoping every statement to that tenant, rather than crossing
	// tenants, where a statement missing its condition reaches every tenant's
	// rows. Disabling a tenant keeps its requests and Enter out; it does not
	// keep out a path that names it here, which is why this is a method of its
	// own, recognisable at the call site, rather than an option of Enter's.
	EnterRegistered(ctx context.Context, id ID) (context.Context, error)
}
