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
	// name that is empty or blanks alone, or a nil implementation.
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
	// tenant that is not there is told.
	//
	// The cases are one sentinel because the caller's move is the same either
	// way: check the value it passed, against the shape and against the
	// roster. They are told apart in the message.
	//
	// A roster row holding such a tenant is reported through this sentinel
	// too, by the read that refuses it. The table can be written by something
	// other than Put, and an identifier that reached the snapshot would reach
	// every caller that resolves a tenant to it.
	ErrInvalidTenant = errors.New("tenant: a tenant this module will not act on")

	// ErrAliasConflict reports a write that would put one alias on two
	// tenants. The fix is to change one of them, or to rename the tenant
	// that already holds it.
	ErrAliasConflict = errors.New("tenant: two tenants would share one alias")

	// ErrNoTenantInContext reports a statement against a scoped model whose
	// context carries no tenant. The fix is to put one there — through a
	// request whose endpoint this module guards, or by the caller that
	// built the context — or to state the intent to cross tenants.
	ErrNoTenantInContext = errors.New("tenant: a scoped statement has no tenant in its context")

	// ErrExplicitTenantMismatch reports a write that names a tenant column
	// value other than the one its context carries. Crossing tenants is a
	// context fact with one way in, so a statement naming a second tenant
	// is a call-site defect rather than a way to cross.
	ErrExplicitTenantMismatch = errors.New("tenant: a write names a tenant other than its context's")
)
