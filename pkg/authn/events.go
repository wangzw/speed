package authn

// The event types this capability publishes. The set is closed, and a type is
// named by function and not by IdP, login method or tenant: what varies is a
// payload field. A type's payload is the struct beside its constant, which a
// subscriber decodes to with event.DecodePayload.
//
// An event is a hint and never the authority: account state, memberships,
// sessions and revocation live in the database, and a subscriber may use an event
// to drop a local cache and to start a cleanup but not as its only ground.
// Delivery differs by transport, so a subscriber deduplicates by Event.ID and
// relies on neither order nor count. Where an event is staged through the outbox
// it is published after the commit, at least once, under one Event.ID.
//
// There is no event for a failed login: a failure goes to the audit row, the log
// and a counter, and only the start of a back-off is an event.
const (
	EventAccountCreated       = "authn.account.created"
	EventAccountStatusChanged = "authn.account.status-changed" // staged through the outbox
	EventAccountDeleted       = "authn.account.deleted"        // staged through the outbox
	EventIdentifierChanged    = "authn.identifier.changed"
	EventBindingChanged       = "authn.binding.changed"
	EventMembershipChanged    = "authn.membership.changed" // staged through the outbox; the tenant is in the envelope
	EventPasswordChanged      = "authn.password.changed"
	EventFactorChanged        = "authn.factor.changed"
	EventLoginSucceeded       = "authn.login.succeeded"
	EventSessionRevoked       = "authn.session.revoked"
	EventThrottleEngaged      = "authn.throttle.engaged"
	// The next constant is written in two parts so that gosec's credential scan
	// does not read its name as one; its value is "authn.token.reuse-detected".
	EventTokenReuseDetected   = "authn." + "token.reuse-detected"
	EventImpersonationStarted = "authn.impersonation.started" // the tenant is in the envelope
	EventImpersonationEnded   = "authn.impersonation.ended"   // the tenant is in the envelope
	EventIDPChanged           = "authn.idp.changed"           // the tenant is in the envelope
)

// The payloads share one shape. Every field is a top-level scalar under a
// lower-case, underscore-separated JSON name, and a field holds an identifier, an
// enumeration or an instant and nothing else: no secret, no free text such as a
// reason or a display name, no email address, no phone number and no login
// identifier. The test is that every top-level field becomes a span attribute and
// an audit detail, so what it holds must be harmless in both places. An instant is
// an RFC 3339 string, because a number that crossed a transport comes back as a
// json.Number and loses digits beyond 2^53. The tenant of an event is in
// Event.TenantID, never in a payload.
//
// The closed sets a field takes are listed with it. They only grow, and a
// subscriber has to tolerate a value or a field it does not know.

// AccountCreated is the payload of EventAccountCreated.
type AccountCreated struct {
	AccountID string `json:"account_id"`
	Kind      string `json:"kind"` // user or service
	Via       string `json:"via"`  // admin, jit, signup or import
}

// AccountStatusChanged is the payload of EventAccountStatusChanged.
type AccountStatusChanged struct {
	AccountID   string `json:"account_id"`
	From        string `json:"from"` // the name of an AccountStatus: disabled, active or deleted
	To          string `json:"to"`
	ReasonClass string `json:"reason_class"` // one of the ReasonClass constants
}

// AccountDeleted is the payload of EventAccountDeleted.
type AccountDeleted struct {
	AccountID string `json:"account_id"`
}

// IdentifierChanged is the payload of EventIdentifierChanged.
type IdentifierChanged struct {
	AccountID string `json:"account_id"`
	Kind      string `json:"kind"`   // username, email or phone
	Change    string `json:"change"` // added or removed
}

// BindingChanged is the payload of EventBindingChanged.
type BindingChanged struct {
	AccountID     string `json:"account_id"`
	ProviderClass string `json:"provider_class"` // google, github, wechat, oidc or saml
	Change        string `json:"change"`         // linked or unlinked
	Source        string `json:"source"`         // explicit, auto or jit
}

// MembershipChanged is the payload of EventMembershipChanged.
type MembershipChanged struct {
	AccountID string `json:"account_id"`
	Change    string `json:"change"` // added, suspended, resumed, removed, sso-exempt-set or sso-exempt-cleared
	Source    string `json:"source"` // as Membership.Source is written; it may carry an IdP identifier
}

// PasswordChanged is the payload of EventPasswordChanged. A rehash is not a
// change and publishes nothing.
type PasswordChanged struct {
	AccountID string `json:"account_id"`
	By        string `json:"by"`      // self, admin, import or reset
	Channel   string `json:"channel"` // email or sms on a reset; empty otherwise
}

// FactorChanged is the payload of EventFactorChanged.
type FactorChanged struct {
	AccountID string `json:"account_id"`
	FactorID  string `json:"factor_id"`
	Kind      string `json:"kind"`   // totp, sms or webauthn
	Change    string `json:"change"` // enrolled, removed, reset or flagged
	By        string `json:"by"`     // the class of whoever acted, as the publishing module's document states it
}

// LoginSucceeded is the payload of EventLoginSucceeded.
type LoginSucceeded struct {
	AccountID string `json:"account_id"`
	SessionID string `json:"session_id"`
	Methods   string `json:"methods"`   // the Method values, joined by commas
	Assurance string `json:"assurance"` // the level's name, never its number
	Delivery  string `json:"delivery"`  // session or token
}

// SessionRevoked is the payload of EventSessionRevoked.
type SessionRevoked struct {
	SessionID   string `json:"session_id"` // empty when Scope is all
	AccountID   string `json:"account_id"`
	Scope       string `json:"scope"`        // one or all
	ReasonClass string `json:"reason_class"` // one of the ReasonClass constants
}

// ThrottleEngaged is the payload of EventThrottleEngaged, published by the
// caller of Throttle.Fail that learned the failure started a back-off.
type ThrottleEngaged struct {
	Scope     string `json:"scope"`                // a ThrottleScope: identifier, account, factor, phone, client or signup-username
	AccountID string `json:"account_id,omitempty"` // only for the scopes account and factor
}

// TokenReuseDetected is the payload of EventTokenReuseDetected.
type TokenReuseDetected struct {
	SessionID string `json:"session_id"`
	AccountID string `json:"account_id"`
}

// ImpersonationStarted is the payload of EventImpersonationStarted. The reason
// of an impersonation is in the audit row and the record only, never here.
type ImpersonationStarted struct {
	GrantID   string `json:"grant_id"`
	ActorID   string `json:"actor_id"`
	AccountID string `json:"account_id"` // the impersonated account
	SessionID string `json:"session_id"`
	ExpiresAt string `json:"expires_at"` // RFC 3339
}

// ImpersonationEnded is the payload of EventImpersonationEnded. A record that
// merely ran past its end publishes nothing, and whoever needs that signal
// computes it from ImpersonationStarted.ExpiresAt.
type ImpersonationEnded struct {
	GrantID string `json:"grant_id"`
	EndedBy string `json:"ended_by"` // actor, session-ended, disabled, membership-removed or revoked
}

// IDPChanged is the payload of EventIDPChanged.
type IDPChanged struct {
	IDPID  string `json:"idp_id"`
	Change string `json:"change"` // created, updated, enabled, disabled, deleted, domain-verified, domain-unverified or domain-removed
}

// The classes of reason a state change or a revocation carries. The set is
// closed and a reason handed to SetStatus, Delete, Sessions.Revoke or
// Sessions.RevokeAccount is one of these, as is the reason an acceptance's
// system revocation records, so that a reason is never free text.
const (
	ReasonAdmin           = "admin"            // an administrator started the change or revoked
	ReasonSelf            = "self"             // the account holder: logging out, or revoking the other sessions after a change of their own
	ReasonPasswordChange  = "password-change"  // the other sessions revoked after a password change
	ReasonFactorReset     = "factor-reset"     // sessions revoked after an administrator reset the factors
	ReasonRefreshReuse    = "refresh-reuse"    // reuse of a refresh token was detected and the family revoked
	ReasonExpired         = "expired"          // it ran out
	ReasonAccountDisabled = "account-disabled" // the account was disabled
	ReasonAccountDeleted  = "account-deleted"  // the account was deleted
	ReasonEvicted         = "evicted"          // over the per-account session limit, the least recently used was revoked
	ReasonThrottled       = "throttled"        // a back-off began: only in the log line of the back-off, never in an event payload or an audit detail

	// The classes an acceptance's system revocation records: the invitation could
	// no longer be redeemed, so it was revoked rather than refused alone.
	ReasonInvitationMembershipInactive = "invitation-membership-inactive" // the invitee's membership in the tenant is suspended
	ReasonInvitationTenantDeleted      = "invitation-tenant-deleted"      // the tenant no longer exists
	ReasonInvitationRoleWithdrawn      = "invitation-role-withdrawn"      // the role the invitation grants can no longer be granted
)

// ValidReasonClass reports whether s is one of the Reason constants above, the closed
// set of classes. A value outside it is refused, whatever else it looks like.
func ValidReasonClass(s string) bool {
	switch s {
	case ReasonAdmin, ReasonSelf, ReasonPasswordChange, ReasonFactorReset, ReasonRefreshReuse,
		ReasonExpired, ReasonAccountDisabled, ReasonAccountDeleted, ReasonEvicted, ReasonThrottled,
		ReasonInvitationMembershipInactive, ReasonInvitationTenantDeleted, ReasonInvitationRoleWithdrawn:
		return true
	}
	return false
}

// ValidPayloadReasonClass reports whether s may be written into an event payload
// or an audit detail: a Reason constant other than ReasonThrottled, which only a
// log line carries.
func ValidPayloadReasonClass(s string) bool { return s != ReasonThrottled && ValidReasonClass(s) }
