package tenant

import "errors"

var (
	// ErrNoRouter reports that Init found no HTTP entry point to register a
	// layer on.
	ErrNoRouter = errors.New("tenant: no HTTP entry point in this command")

	// ErrUnknownEndpoint reports an endpoint named in the configuration that
	// the entry point does not have.
	ErrUnknownEndpoint = errors.New("tenant: the endpoint list names an endpoint that does not exist")

	// ErrDuplicateEndpoint reports an endpoint named twice in the configuration;
	// the list is a set. The message names the endpoint.
	ErrDuplicateEndpoint = errors.New("tenant: the endpoint list names an endpoint twice")

	// ErrNoExtractor reports that the configured extractor name matches no
	// registration when the window closes.
	ErrNoExtractor = errors.New("tenant: the configured extractor is not registered")

	// ErrExtractorConflict reports two extractors registered under one name.
	ErrExtractorConflict = errors.New("tenant: two extractors are registered under one name")

	// ErrInvalidExtractor reports a malformed registration: an empty name, a name
	// carrying a blank, or a nil implementation.
	ErrInvalidExtractor = errors.New("tenant: an extractor registration is not well formed")

	// ErrRosterMissing reports that the roster table is not there — never created or dropped since —
	// or has not been read yet. The failed statement's error stays in the chain; a failure for another
	// reason, such as a lock, a lost connection or a refused permission, does not carry it.
	ErrRosterMissing = errors.New("tenant: the roster table does not exist")

	// ErrNoConnection reports a roster statement that got no connection of the
	// delivered pool within the database module's Connection bound and did not
	// run.
	ErrNoConnection = errors.New("tenant: the roster got no connection of the delivered database in time")

	// ErrInvalidTenant reports a tenant this module will not act on: a value outside
	// its shape, or an identifier no roster row carries. The cases share one sentinel
	// because the caller's move is the same: check the shape and the roster.
	ErrInvalidTenant = errors.New("tenant: a tenant this module will not act on")

	// ErrInvalidProfile reports an organization profile outside its field bounds
	// or shapes. The message names the field, never the value.
	ErrInvalidProfile = errors.New("tenant: an organization profile this module will not act on")

	// ErrTenantDisabled reports a tenant the roster carries but does not serve,
	// asked to be entered.
	ErrTenantDisabled = errors.New("tenant: the tenant is not being served")

	// ErrTenantDeleted reports a tombstone: a write path asked to change one, or
	// Enter asked to enter one.
	ErrTenantDeleted = errors.New("tenant: the tenant has been deleted")

	// ErrNameConflict reports a write that would put one slug on two tenants: a
	// name another tenant carries, or a name that is another tenant's identifier.
	ErrNameConflict = errors.New("tenant: two tenants would share one name")

	// ErrTenantExists reports a creation naming an identifier the roster's table
	// already holds, as some tenant's identifier or as its name. Nothing was
	// written.
	ErrTenantExists = errors.New("tenant: a tenant with this identifier is already registered")

	// ErrNoTenantInContext reports a scoped statement whose context carries no
	// tenant, or a model or join table from which no one tenant column can be
	// taken.
	ErrNoTenantInContext = errors.New("tenant: a scoped statement has no tenant in its context")

	// ErrExplicitTenantMismatch reports a write naming a tenant column value
	// other than its context's, or one that cannot be read as that value at all.
	ErrExplicitTenantMismatch = errors.New("tenant: a write names a tenant other than its context's")
)
