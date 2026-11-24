package tenant

import "errors"

// Sentinels for the failures this capability directory judges for itself. A caller
// tells them apart with errors.Is.
var (
	// ErrNoRouter reports that Init found no HTTP entry point to register a
	// layer on; the command must list the entry point module.
	ErrNoRouter = errors.New("tenant: no HTTP entry point in this command")

	// ErrUnknownEndpoint reports that the configured endpoint list names an
	// endpoint the entry point does not have.
	ErrUnknownEndpoint = errors.New("tenant: the endpoint list names an endpoint that does not exist")

	// ErrDuplicateEndpoint reports that the configured endpoint list names one
	// endpoint twice. The message names the endpoint.
	ErrDuplicateEndpoint = errors.New("tenant: the endpoint list names an endpoint twice")

	// ErrNoExtractor reports that the configured extractor name matches no
	// registration by the time the registration window closes.
	ErrNoExtractor = errors.New("tenant: the configured extractor is not registered")

	// ErrExtractorConflict reports two registrations under one name. The message
	// names the name and the two implementation types.
	ErrExtractorConflict = errors.New("tenant: two extractors are registered under one name")

	// ErrInvalidExtractor reports a registration with an empty name, a name
	// carrying a blank, or a nil implementation.
	ErrInvalidExtractor = errors.New("tenant: an extractor registration is not well formed")

	// ErrRosterMissing reports that the roster table does not exist, or that the
	// roster has not been read yet. The failed statement's own error is kept in the
	// chain; a failure for any other reason does not carry it.
	ErrRosterMissing = errors.New("tenant: the roster table does not exist")

	// ErrNoConnection reports a roster statement that got no connection of the
	// delivered pool within five seconds, so the read or write did not happen. It
	// is not an answer about the tenant.
	ErrNoConnection = errors.New("tenant: the roster got no connection of the delivered database in time")

	// ErrInvalidTenant reports a tenant this module will not act on: a value of
	// the wrong shape, an identifier no roster row carries, or a roster row holding
	// an out-of-domain tenant. The message tells the cases apart.
	ErrInvalidTenant = errors.New("tenant: a tenant this module will not act on")

	// ErrInvalidProfile reports an organization profile outside its bounds or
	// shape. The message names the field and never the value.
	ErrInvalidProfile = errors.New("tenant: an organization profile this module will not act on")

	// ErrTenantDisabled reports an attempt to enter a tenant the roster carries
	// but does not serve. Work that belongs to a disabled tenant enters it through
	// Catalog.EnterRegistered.
	ErrTenantDisabled = errors.New("tenant: the tenant is not being served")

	// ErrTenantDeleted reports a write or Enter against a tenant the roster holds
	// as a tombstone. Work that still belongs to it enters through
	// Catalog.EnterRegistered.
	ErrTenantDeleted = errors.New("tenant: the tenant has been deleted")

	// ErrNameConflict reports a write that would put one name on two tenants: a
	// name another tenant carries, or another tenant's identifier. The message names
	// the tenant holding the value.
	ErrNameConflict = errors.New("tenant: two tenants would share one name")

	// ErrTenantExists reports a creation naming an identifier the roster's table
	// already holds, as an identifier or as a name. Nothing was written.
	ErrTenantExists = errors.New("tenant: a tenant with this identifier is already registered")

	// ErrNoTenantInContext reports a scoped statement whose context carries no
	// tenant, a crossing write that leaves the tenant column empty or null, or a
	// statement through a model or join table from which no one tenant column can be
	// taken. The message names the model or table and why.
	ErrNoTenantInContext = errors.New("tenant: a scoped statement has no tenant in its context")

	// ErrExplicitTenantMismatch reports a write that names a tenant column value
	// other than its context's, or one that cannot be read as that value, or a
	// raw assignment target that cannot be read as one column.
	ErrExplicitTenantMismatch = errors.New("tenant: a write names a tenant other than its context's")
)
