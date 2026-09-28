package tenant

import "errors"

// The failures this capability directory judges for itself. A caller tells
// them apart with errors.Is, and every wrap on the way out uses %w so the
// chain stays whole.
//
// The set is closed over what this capability directory produces: a newly
// judged class of failure either joins one of these or earns another. Two
// classes are deliberately outside it.
//
// The first is a module of this capability directory being absent. Both
// modules declare what they need as a construction-time dependency, and
// dependency resolution runs before anything is constructed, so a provider
// that is missing or stood down ends the run there as core's
// ErrMissingProvider. A sentinel here would be a clause nothing can reach.
//
// The second is taking up the configuration reader, which is core's
// ErrMissingProvider too: it means the host never supplied one, which is the
// host's omission rather than a defect this capability directory judged.
var (
	// ErrNoRouter reports that Init found no HTTP entry point to register a
	// layer on. The fix is to write the entry implementation's module name
	// into this command: a command that resolves tenants and has no entry
	// point has nothing to resolve them for.
	ErrNoRouter = errors.New("tenant: no HTTP entry point in this command")

	// ErrUnknownEndpoint reports that the configured endpoint list names an
	// endpoint the entry point does not have. The fix is to correct the
	// name, or to declare that endpoint in the entry point's configuration.
	ErrUnknownEndpoint = errors.New("tenant: the endpoint list names an endpoint that does not exist")

	// ErrDuplicateEndpoint reports that the configured endpoint list names
	// one endpoint twice. The list is a set — a repeated entry registers the
	// resolution layer on that endpoint a second time — so the fix is to
	// drop the repeat. The message names the endpoint.
	ErrDuplicateEndpoint = errors.New("tenant: the endpoint list names an endpoint twice")

	// ErrNoExtractor reports that the configured extractor name matches no
	// registration by the time the window closes. The fix is to correct the
	// name, or to register an extractor under it.
	ErrNoExtractor = errors.New("tenant: the configured extractor is not registered")

	// ErrExtractorConflict reports two registrations under one name. The fix
	// is to change one of them. The registration surface is handed no
	// identity, so the message names the name and the two implementation
	// types rather than the modules that registered them.
	ErrExtractorConflict = errors.New("tenant: two extractors are registered under one name")

	// ErrInvalidExtractor reports a registration whose own shape is wrong: a
	// name that is empty or carries a blank anywhere, or a nil implementation.
	ErrInvalidExtractor = errors.New("tenant: an extractor registration is not well formed")

	// ErrRosterMissing reports that the roster table was not there when the
	// snapshot was loaded. The fix is to write the roster subpackage's
	// module name into the command that applies migrations, so that its
	// migration set is collected: a command that reaches it for its
	// declarations alone is the form that does this, and the alternative —
	// taking it in to run — makes it answerable for its own configuration
	// on a command that only builds tables.
	ErrRosterMissing = errors.New("tenant: the roster table does not exist")

	// ErrInvalidTenant reports a tenant this module will not act on: one
	// whose own shape is wrong — an empty identifier or alias, or either
	// carrying blanks or control characters — or an identifier no row of the
	// roster carries, which is what a caller asking to change the state of a
	// tenant that is not there, or to enter one, is told.
	//
	// The cases are one sentinel because the caller's move is the same either
	// way: check the value it passed, against the shape and against the
	// roster. They are told apart in the message.
	//
	// A tenant column given such an identifier is reported through it as
	// well: by a write crossing tenants that names one, and by the identifier
	// itself wherever it is bound into a statement.
	//
	// A roster row holding such a tenant is reported through this sentinel
	// too, by the read that refuses it. The table can be written by something
	// other than Put, and an identifier that reached the snapshot would reach
	// every caller that resolves a tenant to it.
	ErrInvalidTenant = errors.New("tenant: a tenant this module will not act on")

	// ErrTenantDisabled reports a tenant the roster carries but does not
	// serve, asked to be entered. The fix is the tenant's state, not the call:
	// a path acting for a disabled tenant has nothing to act on until the
	// tenant is served again.
	//
	// It is apart from ErrInvalidTenant, which an identifier no row carries
	// gets, because a caller's move differs: an unknown identifier is a defect
	// in whatever handed it over, a disabled one is a state the work waits out
	// or drops. The request face answers both alike, and for a reason that
	// does not hold here: that answer goes to a caller outside the process,
	// and this one to the process's own code.
	ErrTenantDisabled = errors.New("tenant: the tenant is not being served")

	// ErrAliasConflict reports a write that would put one alias on two
	// tenants. The fix is to change one of them, or to rename the tenant
	// that already holds it.
	ErrAliasConflict = errors.New("tenant: two tenants would share one alias")

	// ErrTenantExists reports a creation naming an identifier the roster's
	// table already holds. Nothing was written. The fix depends on what the
	// caller meant: a new tenant needs an identifier of its own, and a change
	// to the one that exists goes through Put or SetStatus.
	ErrTenantExists = errors.New("tenant: a tenant with this identifier is already registered")

	// ErrNoTenantInContext reports a statement against a scoped model whose
	// context carries no tenant. The fix is to put one there — through a
	// request whose endpoint this module guards, or by entering the tenant
	// through the roster on a path with no request behind it — or to state
	// the intent to cross tenants.
	//
	// A row that would reach the table with no tenant is reported through
	// this sentinel too: a write crossing tenants that leaves the tenant
	// column empty or null, and an empty identifier bound into a statement
	// on a handle no filter is installed on. The context of either carries
	// no tenant, and the fix is the same — name one.
	ErrNoTenantInContext = errors.New("tenant: a scoped statement has no tenant in its context")

	// ErrExplicitTenantMismatch reports a write that names a tenant column
	// value other than the one its context carries, or one that cannot be
	// read as that value at all — an expression, a subquery, a null. Crossing
	// tenants is a context fact with one way in, so a statement naming a
	// second tenant is a call-site defect rather than a way to cross.
	//
	// The tenant column is whatever the database would take an assignment's
	// target for: a key in any case, carrying its own quotes, or written as it
	// stands with blanks around it. A target written as it stands that cannot
	// be read as one column at all is reported through this sentinel as well,
	// under any context, crossing tenants included: it may be the tenant
	// column, and what it writes there cannot be told. The fix is to name the
	// column alone.
	ErrExplicitTenantMismatch = errors.New("tenant: a write names a tenant other than its context's")
)
