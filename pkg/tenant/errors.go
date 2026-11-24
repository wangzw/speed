package tenant

import "errors"

var (
	// ErrNoRouter reports that Init found no HTTP entry point to register a layer on.
	ErrNoRouter = errors.New("tenant: no HTTP entry point in this command")

	// ErrUnknownEndpoint reports that the configured endpoint list names an
	// endpoint the entry point does not have.
	ErrUnknownEndpoint = errors.New("tenant: the endpoint list names an endpoint that does not exist")

	// ErrDuplicateEndpoint reports that the configured endpoint list names one
	// endpoint twice.
	ErrDuplicateEndpoint = errors.New("tenant: the endpoint list names an endpoint twice")

	// ErrNoExtractor reports that the configured extractor name matches no
	// registration once the registration window closes.
	ErrNoExtractor = errors.New("tenant: the configured extractor is not registered")

	// ErrExtractorConflict reports two registrations under one name.
	ErrExtractorConflict = errors.New("tenant: two extractors are registered under one name")

	// ErrInvalidExtractor reports a registration with an empty or blank-bearing
	// name, or a nil implementation.
	ErrInvalidExtractor = errors.New("tenant: an extractor registration is not well formed")

	// ErrRosterMissing reports that the roster table does not exist, or that the
	// roster has not been read yet. The statement's own failure is kept in the chain.
	ErrRosterMissing = errors.New("tenant: the roster table does not exist")

	// ErrNoConnection reports a roster statement that got no connection of the
	// delivered pool in time and therefore did not run. It is neither refusal.
	ErrNoConnection = errors.New("tenant: the roster got no connection of the delivered database in time")

	// ErrInvalidTenant reports a tenant this module will not act on: a malformed
	// identifier, name or owner, a rename, or an identifier no roster row carries.
	ErrInvalidTenant = errors.New("tenant: a tenant this module will not act on")

	// ErrInvalidProfile reports an organization profile outside its domain. The
	// message names the field, never the value.
	ErrInvalidProfile = errors.New("tenant: an organization profile this module will not act on")

	// ErrTenantDisabled reports a tenant the roster carries but does not serve,
	// asked to be entered.
	ErrTenantDisabled = errors.New("tenant: the tenant is not being served")

	// ErrTenantDeleted reports a tenant the roster holds as a tombstone, asked to
	// be changed or entered.
	ErrTenantDeleted = errors.New("tenant: the tenant has been deleted")

	// ErrNameConflict reports a write that would put one slug on two tenants. The
	// message names the tenant holding the value.
	ErrNameConflict = errors.New("tenant: two tenants would share one name")

	// ErrTenantExists reports a creation naming an identifier the roster already
	// holds, as an identifier or as a name. Nothing was written.
	ErrTenantExists = errors.New("tenant: a tenant with this identifier is already registered")

	// ErrNoTenantInContext reports a scoped statement whose context carries no
	// tenant, or one through a model or join table no single tenant column can
	// be taken from.
	ErrNoTenantInContext = errors.New("tenant: a scoped statement has no tenant in its context")

	// ErrExplicitTenantMismatch reports a write that names a tenant column value
	// other than its context's, or one that cannot be read as that value.
	ErrExplicitTenantMismatch = errors.New("tenant: a write names a tenant other than its context's")
)
