package authn

import (
	"errors"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// The failures this capability directory judges for itself. A caller tells
// them apart with errors.Is, and every wrap on the way out uses %w so the
// chain stays whole.
//
// The set is closed over what this capability directory produces: a newly
// judged class of failure either joins one of these or earns another, and none
// of them is invented for a failure that merely passed through. A backend's own
// errors are its own to name; the gateway carries them out unchanged.
//
// Two sentinels are declared beside the shape they belong to rather than here:
// ErrInvalidAccountID with AccountID, and ErrThrottled with Throttle. What is
// declared here is what has no shape of its own to sit beside, whether it is
// judged at startup or while serving: ErrBusy is raised by the password
// derivation governor, an internal package that imports this one and so cannot
// hold the declaration, and ErrInvalidArgument is raised by the attribute checks
// of the model package, which imports nothing of this directory.
//
// Taking up the configuration reader is the exception that stays outside: a
// reader that is not there means the host never supplied one, which is core's
// ErrMissingProvider and is carried out as it stands. The fix is the host's,
// not a defect this capability directory judged.
var (
	// ErrNoRouter reports that Init found no HTTP entry point to register
	// on. The fix is to write the entry implementation's module name into
	// this command: a command that includes authentication and no entry
	// point has nothing to authenticate.
	ErrNoRouter = errors.New("authn: no HTTP entry point in this command")

	// ErrNoVerifier reports that Init took up no credential-verifying
	// backend. The fix is to write the chosen backend's module name into
	// this command: a backend that was named but cannot start has already
	// stopped the startup with its own failure, so reaching here means no
	// backend was named at all.
	ErrNoVerifier = errors.New("authn: no credential-verifying backend in this command")

	// ErrClaimConflict reports two backends claiming the same carrier key:
	// one name reported by both, one authentication scheme or one cookie name
	// declared by both, or two non-empty account prefixes of which one begins
	// with the other. The failure names both backends and the key they
	// collide on. The fix is to change one of the two declarations.
	ErrClaimConflict = errors.New("authn: two backends claim the same name, scheme, cookie or account prefix")

	// ErrInvalidClaim reports a backend declaring something whose own shape is
	// wrong: an empty name or one not spelt [a-z0-9][a-z0-9.-]*, a scheme that
	// is not an RFC 9110 token, a cookie name that is not a cookie name, no
	// carrier at all, an account prefix a backend may not declare, or, for the
	// static credential table, an empty table, an entry name that is not one,
	// or an entry that cannot be read, which also wraps static.ErrMalformedHash
	// so that either sentinel matches with errors.Is. The failure names the
	// backend and what is wrong.
	ErrInvalidClaim = errors.New("authn: a backend's declaration is not well formed")

	// ErrInvalidConfig reports a configuration a module cannot run on: an
	// empty endpoint list, an endpoint named empty or twice, the wildcard "*"
	// written beside a named endpoint, a call budget or in-flight width that
	// is not positive, or an endpoint for authentication routes that the
	// tenant admission layer guards. The failure names the configuration item
	// to correct.
	ErrInvalidConfig = errors.New("authn: the configuration holds a value it cannot run on")

	// ErrUnknownEndpoint reports an endpoint name the entry point does not
	// have, written either into a configuration list or into a route
	// registration. The fix is to correct the name, or to declare that
	// endpoint in the entry point's configuration.
	ErrUnknownEndpoint = errors.New("authn: endpoint name is not one the entry point declares")

	// ErrExemptionUnguarded reports a route registration, Exempt, Optional or
	// Impersonable, on an endpoint the gateway does not guard. Left
	// unreported the registration would be swallowed: the direction is
	// closed, but nothing would say so. The fix is to write the endpoint
	// into the gateway's endpoint list, or to move the registration.
	ErrExemptionUnguarded = errors.New("authn: route registration lands on an endpoint this gateway does not guard")

	// ErrAccessConflict reports one route, the same endpoint, method and
	// path, registered both Exempt and Optional, or both Exempt and
	// Impersonable. The fix is to drop one of the two.
	ErrAccessConflict = errors.New("authn: one route is registered with two access levels that exclude each other")

	// ErrRoutesUnconfigured reports a module that mounts routes finding no
	// endpoint to mount them on: none is configured and the entry point
	// declares not exactly one endpoint, zero or several. The fix is to
	// configure the mount endpoint.
	ErrRoutesUnconfigured = errors.New("authn: no endpoint is configured to mount routes on")

	// ErrRoutesUnguarded reports the endpoint authentication routes are
	// mounted on not being one the gateway guards. The fix is to write it
	// into the gateway's endpoint list.
	ErrRoutesUnguarded = errors.New("authn: the endpoint for authentication routes is not one the gateway guards")

	// ErrInsecureConfig reports a public base URL that is not https while
	// insecure use was not allowed, or a same-site setting that is none. The
	// fix is to change the address, or to allow insecure use explicitly,
	// which is for development.
	ErrInsecureConfig = errors.New("authn: the configuration weakens a protection it has not been told to give up")

	// ErrBusy reports that a password derivation was refused because the
	// limit on concurrent derivations or on their memory was reached. The
	// attempt did not verify anything. A gateway that finds it inside the
	// cause of an Unavailable verdict answers 503 with Retry-After: 1, which
	// no other cause of a 503 carries.
	ErrBusy = errors.New("authn: password derivation capacity is in use")

	// ErrDrainTimeout reports that Close had waited the call budget for the
	// calls inside a backend and some were still there. The calls go on
	// running; what Close gives up is the wait, so that a backend which has
	// stopped answering cannot hold the whole shutdown open.
	ErrDrainTimeout = errors.New("authn: backend calls were still in flight after the call budget")
)

// Startup failures of the modules beyond the gateway. Each ends the start and
// names what is missing or wrong, never a secret.
var (
	// ErrNoEncryptionKey reports that the round trip through an encrypted column
	// failed its self-check: the database module's root key is missing or
	// unusable. The fix is to give the database module an encryption key.
	ErrNoEncryptionKey = errors.New("authn: no usable encryption key for the encrypted columns")

	// ErrInvalidKey reports a signing or verification key that cannot be used: a
	// key file that cannot be read, a type that does not match the algorithm, an
	// RSA key under 2048 bits, a repeated key identifier, or no key at all. The
	// failure names the file.
	ErrInvalidKey = errors.New("authn: a signing or verification key cannot be used")

	// ErrNotMigrated reports that a module's Start found none of its own tables.
	// The fix is to include the module statically in the command that applies
	// the migrations, which is IncludeAllDeclarations.
	ErrNotMigrated = errors.New("authn: the module's tables do not exist")

	// ErrMethodConflict reports two login methods with one name. The failure names
	// both.
	ErrMethodConflict = errors.New("authn: two login methods have the same name")

	// ErrFactorConflict reports two second factors of one kind. The failure names
	// both.
	ErrFactorConflict = errors.New("authn: two factors are of the same kind")

	// ErrNoLoginMethod reports that the login engine was taken in with no login
	// method. The fix is to name a login method's module in the command.
	ErrNoLoginMethod = errors.New("authn: no login method in this command")

	// ErrNoFactor reports that the multi-factor module was taken in with no
	// factor while global-required is set, or while accounts in the store have
	// registered a kind that no module implements. The fix is to name the missing
	// factor's module.
	ErrNoFactor = errors.New("authn: no factor for what the multi-factor policy requires")
)

// Failures of the account interfaces. The messages name a class and never a
// value the caller supplied.
var (
	// ErrAccountNotFound reports an account that does not exist, a tombstone
	// included. Outwardly it is merged into login-failed, or a 404.
	ErrAccountNotFound = errors.New("authn: account not found")

	// ErrAccountExists reports an identifier, a login name or a binding that
	// breaks a uniqueness constraint, which gorm.ErrDuplicatedKey reports the same
	// way on both engines. Outwardly it is a 409.
	ErrAccountExists = errors.New("authn: account, identifier or binding already exists")

	// ErrInvalidCredentials reports an unknown identifier, a wrong password, a
	// wrong code, a disabled account or an account with no password, all in this
	// one value. Outwardly it is a 401 login-failed.
	ErrInvalidCredentials = errors.New("authn: invalid credentials")

	// ErrPasswordPolicy reports a password the policy refuses: too short, too
	// long, equal to the person's own identifier, below the OWASP floor, or equal
	// to the current password. Outwardly it is a 400 bad-request.
	ErrPasswordPolicy = errors.New("authn: password does not meet the policy")

	// ErrInvalidIdentifier reports an identifier that does not normalize: a
	// username, an email address or a phone number of the wrong form. Outwardly it
	// is a 400 bad-request.
	ErrInvalidIdentifier = errors.New("authn: identifier is not well formed")

	// ErrInvalidArgument reports a value a write refused because it is outside what
	// the call admits: an attribute set that breaks its bounds, a reason that is
	// not one of the ReasonClass constants, a Verification that is none of the
	// forms it takes, a display name that is too long. Its text names the class
	// and never the value. Outwardly it is a 400 bad-request on an administration
	// route; a module that meets it in a value it wrote itself, the idp.*
	// attributes of a session payload, has found a defect of its own and answers
	// 503.
	ErrInvalidArgument = model.ErrInvalidArgument

	// ErrIdentifierRequired reports an account with no identifier that can serve as
	// a login name where a password is to be set. Outwardly it is a 409
	// login-identifier-required.
	ErrIdentifierRequired = errors.New("authn: the account has no identifier to log in with")

	// ErrCleanupIncomplete reports that an account's state or a service secret's
	// state is in force and an AccountLifecycle hook failed. Repeating the same
	// call finishes the cleanup. Outwardly it is a 503.
	ErrCleanupIncomplete = errors.New("authn: the state is in force and a cleanup hook failed")

	// ErrBindingConflict reports an external identity that another account holds.
	// Outwardly it is a 409.
	ErrBindingConflict = errors.New("authn: the external identity is bound to another account")

	// ErrLastLoginMethod reports an unlinking that would leave the account no way
	// to log in. Outwardly it is a 409.
	ErrLastLoginMethod = errors.New("authn: this is the account's last way to log in")

	// ErrAccountUnavailable reports a disabled account on an external login path,
	// where the caller has already proved who they are at the provider. Outwardly
	// it is a 403 account-unavailable.
	ErrAccountUnavailable = errors.New("authn: the account is not available")

	// ErrLinkRequired reports a social login with no binding whose verified email
	// address matches an existing account, or an enterprise JIT sign-up that meets
	// an address already taken. Outwardly it is a 401 link-required.
	ErrLinkRequired = errors.New("authn: the external identity has to be linked to an existing account first")

	// ErrNotProvisioned reports an enterprise sign-in with no account an
	// administrator prepared. Outwardly it is a 401 login-failed, and the cause
	// goes to the log.
	ErrNotProvisioned = errors.New("authn: no account was provisioned for this identity")

	// ErrSignupClosed reports a first login at a provider while sign-up is closed.
	// Outwardly it is a 401 login-failed, and the cause goes to the log.
	ErrSignupClosed = errors.New("authn: sign-up is closed")

	// ErrDomainMismatch reports an enterprise sign-in whose email address is in a
	// domain the tenant has not verified. Outwardly it is a 401 login-failed, and
	// the cause goes to the log.
	ErrDomainMismatch = errors.New("authn: the email domain does not belong to the tenant")

	// ErrProvisionRefused is what a Provisioner returns to refuse a sign-up.
	// Outwardly it is a 401 login-failed, and the cause goes to the log.
	ErrProvisionRefused = errors.New("authn: the host refused to create the account")

	// ErrUnavailable reports that an external dependency, the database, the
	// key-value store, a sender or an IdP, cannot be reached. The dependency's own
	// error is wrapped in it and goes to the log. Outwardly it is a 503 that does
	// not tell which. It is not a name for every runtime failure: a caller tells a
	// configuration defect, a refusal and a failing backend apart, because the
	// remedies differ.
	ErrUnavailable = errors.New("authn: a dependency is unavailable")
)

// Failures of sessions, flows and second factors.
var (
	// ErrSessionNotFound reports a session that does not exist, as Sessions.Get
	// and Status say it. The gateway turns it into a 401 and the cause goes to the
	// log.
	ErrSessionNotFound = errors.New("authn: session not found")

	// ErrSessionRevoked reports that Sessions.ForCredential met a revoked service
	// session. The token is not exchanged, and the token endpoint states the
	// failure in its own terms.
	ErrSessionRevoked = errors.New("authn: session was revoked")

	// ErrCSRF reports a cross-origin check that failed. The gateway turns it into
	// a 401 and the cause goes to the log.
	ErrCSRF = errors.New("authn: cross-origin check failed")

	// ErrFlowInvalid reports a login flow that is expired, used, bound to someone
	// else, or consumed by a concurrent call. Outwardly it is a 401 flow-invalid.
	ErrFlowInvalid = errors.New("authn: the flow is not valid")

	// ErrStepOutOfOrder reports an answer that does not belong to the flow's
	// current step. Outwardly it is a 401 flow-invalid.
	ErrStepOutOfOrder = errors.New("authn: the answer is out of order")

	// ErrMethodDisabled reports a login method that disabled-methods has turned
	// off. Outwardly it is a 403 method-disabled.
	ErrMethodDisabled = errors.New("authn: the login method is disabled")

	// ErrFactorLimit reports a kind of factor at its per-kind maximum. Outwardly it
	// is a 409 conflict.
	ErrFactorLimit = errors.New("authn: the account has the most factors of this kind it may")

	// ErrLastFactor reports the removal of the last factor of an account that
	// requires one. Outwardly it is a 409 conflict.
	ErrLastFactor = errors.New("authn: this is the account's last factor")

	// ErrFactorNotEnrolled reports a factor that is not registered, or one still
	// pending that was used to verify. Outwardly it is merged into login-failed.
	ErrFactorNotEnrolled = errors.New("authn: the factor is not enrolled")

	// ErrReplay reports a TOTP counter or a WebAuthn signature count that replays.
	// Outwardly it is merged into login-failed.
	ErrReplay = errors.New("authn: replay of a one-time value")

	// ErrFactorFlagged reports a factor whose credential is suspected of being a
	// clone: the assertion is refused, the multi-factor module disables the factor
	// and announces the change as flagged. Outwardly it is merged into login-failed.
	ErrFactorFlagged = errors.New("authn: the factor's credential was flagged as a possible clone")

	// ErrStepUpRequired reports an assurance level, a freshness, a local-only
	// requirement or a method set that the identity does not meet. Outwardly it is
	// a 401 insufficient_user_authentication.
	ErrStepUpRequired = errors.New("authn: a stronger or fresher authentication is required")

	// ErrForbidden reports that a Decider refused, or that none is there.
	// Outwardly it is a 403 forbidden.
	ErrForbidden = errors.New("authn: the action is not allowed")

	// ErrAuditRequired reports that the mandatory audit row of an action could not
	// be written, so the action was not carried out. Outwardly it is a 503.
	ErrAuditRequired = errors.New("authn: the audit row of the action could not be written")
)

// Failures of tokens.
var (
	// ErrTokenInvalid reports an access token that is not valid. The gateway
	// turns it into a 401 and the cause goes to the log.
	ErrTokenInvalid = errors.New("authn: the access token is not valid")

	// ErrInvalidGrant reports a refresh token that is not valid or already used,
	// and a session of a kind that has no refresh token. At the token endpoint it
	// is invalid_grant.
	ErrInvalidGrant = errors.New("authn: the grant is not valid")

	// ErrRefreshReuse reports that a refresh token which was already used was
	// presented past the grace: the whole family of the session has been revoked.
	// At the token endpoint it is invalid_grant.
	ErrRefreshReuse = errors.New("authn: a used refresh token was presented again")

	// ErrNoSigningKey reports an issue with no signing key configured. A caller on
	// the issuing path answers 503.
	ErrNoSigningKey = errors.New("authn: no signing key is configured")

	// ErrReservedClaim reports a ClaimsContributor that returned a reserved claim
	// name. The issue fails as a whole. It is a defect in the host's
	// implementation.
	ErrReservedClaim = errors.New("authn: a claims contributor returned a reserved claim")
)

// Failures of impersonation.
var (
	// ErrImpersonationRestricted reports a start that breaks a restriction on the
	// impersonator, the target or the parameters, without saying which, and a
	// route of this capability refused while an impersonation is on. Outwardly it
	// is a 403 impersonation-restricted.
	ErrImpersonationRestricted = errors.New("authn: the impersonation is restricted")

	// ErrImpersonationActive reports a session that already has an unended valid
	// record. Outwardly it is a 409.
	ErrImpersonationActive = errors.New("authn: an impersonation is already active on this session")

	// ErrNotImpersonating reports an end with no unended record on the session, a
	// revoke of a record that does not exist, and a query on a session with no
	// valid record. Outwardly it is a 404.
	ErrNotImpersonating = errors.New("authn: there is no impersonation")

	// ErrImpersonationNeedsSession reports a caller with no session to bind to.
	// Outwardly it is a 403 impersonation-restricted, the same as
	// ErrImpersonationRestricted.
	ErrImpersonationNeedsSession = errors.New("authn: the impersonation needs a session to bind to")
)

// Failures of SMS and of enterprise SSO.
var (
	// ErrUnsupportedRegion reports a phone number whose prefix is not in the
	// allowed table. Outwardly it is a 400 unsupported-region.
	ErrUnsupportedRegion = errors.New("authn: the phone number's region is not supported")

	// ErrSMSThrottled reports a sender that limited the call. It is not shown to
	// the client: it goes to a metric and the log, and the answer to the request
	// that triggered the send does not change.
	ErrSMSThrottled = errors.New("authn: the SMS sender limited the call")

	// ErrSMSRejected reports a template, a signature or a number the sender
	// refused. It is not shown to the client.
	ErrSMSRejected = errors.New("authn: the SMS sender rejected the message")

	// ErrSMSUncertain reports a send that timed out or failed in a way that leaves
	// delivery unknown, which must not be resent blindly. It is not shown to the
	// client.
	ErrSMSUncertain = errors.New("authn: whether the SMS was delivered is not known")

	// ErrDomainClaimed reports an email domain that another IdP has verified.
	// Outwardly it is a 409.
	ErrDomainClaimed = errors.New("authn: the domain is verified for another identity provider")

	// ErrAssertionReplay reports a SAML assertion identifier that was seen
	// already. The login fails, and a Warn and a metric record it.
	ErrAssertionReplay = errors.New("authn: the assertion was already used")
)
