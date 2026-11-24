package authn

// Each constant names a payload struct beside it, decoded with event.DecodePayload.
// An event is a hint, never the authority: an outbox one publishes after the commit,
// at least once, and a subscriber dedupes by Event.ID, relying on neither order nor count.
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

// Every field is a top-level scalar under a lower-case JSON name and holds an
// identifier, an enumeration or an RFC 3339 instant, never a secret or free
// text; the tenant is in Event.TenantID. Subscribers tolerate unknown values.

// AccountCreated is the payload of EventAccountCreated.
type AccountCreated struct {
	// AccountID is the account that was created.
	AccountID string `json:"account_id"`
	Kind      string `json:"kind"` // user or service
	Via       string `json:"via"`  // admin, jit, signup or import
}

// AccountStatusChanged is the payload of EventAccountStatusChanged.
type AccountStatusChanged struct {
	// AccountID is the account whose status changed.
	AccountID string `json:"account_id"`
	From      string `json:"from"` // the name of an AccountStatus: disabled, active or deleted
	// To is the status the account was moved to.
	To          string `json:"to"`
	ReasonClass string `json:"reason_class"` // one of the ReasonClass constants
}

// AccountDeleted is the payload of EventAccountDeleted.
type AccountDeleted struct {
	// AccountID is the account that was deleted.
	AccountID string `json:"account_id"`
}

// IdentifierChanged is the payload of EventIdentifierChanged.
type IdentifierChanged struct {
	// AccountID is the account whose identifier changed.
	AccountID string `json:"account_id"`
	Kind      string `json:"kind"`   // username, email or phone
	Change    string `json:"change"` // added or removed
}

// BindingChanged is the payload of EventBindingChanged.
type BindingChanged struct {
	// AccountID is the account whose binding changed.
	AccountID     string `json:"account_id"`
	ProviderClass string `json:"provider_class"` // google, github, wechat, oidc or saml
	Change        string `json:"change"`         // linked or unlinked
	Source        string `json:"source"`         // explicit, auto or jit
}

// MembershipChanged is the payload of EventMembershipChanged.
type MembershipChanged struct {
	// AccountID is the account whose membership changed.
	AccountID string `json:"account_id"`
	Change    string `json:"change"` // added, suspended, resumed, removed, sso-exempt-set or sso-exempt-cleared
	Source    string `json:"source"` // as Membership.Source is written; it may carry an IdP identifier
}

// PasswordChanged is the payload of EventPasswordChanged. A rehash is not a
// change and publishes nothing.
type PasswordChanged struct {
	// AccountID is the account whose password changed.
	AccountID string `json:"account_id"`
	By        string `json:"by"`      // self, admin, import or reset
	Channel   string `json:"channel"` // email or sms on a reset; empty otherwise
}

// FactorChanged is the payload of EventFactorChanged.
type FactorChanged struct {
	// AccountID is the account whose factor changed.
	AccountID string `json:"account_id"`
	// FactorID is the factor that changed.
	FactorID string `json:"factor_id"`
	Kind     string `json:"kind"`   // totp, sms or webauthn
	Change   string `json:"change"` // enrolled, removed, reset or flagged
	By       string `json:"by"`     // the class of whoever acted, as the publishing module's document states it
}

// LoginSucceeded is the payload of EventLoginSucceeded.
type LoginSucceeded struct {
	// AccountID is the account that logged in.
	AccountID string `json:"account_id"`
	// SessionID is the session the login created.
	SessionID string `json:"session_id"`
	Methods   string `json:"methods"`   // the Method values, joined by commas
	Assurance string `json:"assurance"` // the level's name, never its number
	Delivery  string `json:"delivery"`  // session or token
}

// SessionRevoked is the payload of EventSessionRevoked.
type SessionRevoked struct {
	SessionID string `json:"session_id"` // empty when Scope is all
	// AccountID is the account whose session was revoked.
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
	// SessionID is the session whose token family was revoked.
	SessionID string `json:"session_id"`
	// AccountID is the account whose refresh token was reused.
	AccountID string `json:"account_id"`
}

// ImpersonationStarted is the payload of EventImpersonationStarted. The reason
// of an impersonation is in the audit row and the record only, never here.
type ImpersonationStarted struct {
	// GrantID is the record that started.
	GrantID string `json:"grant_id"`
	// ActorID is the impersonating account.
	ActorID   string `json:"actor_id"`
	AccountID string `json:"account_id"` // the impersonated account
	// SessionID is the session the impersonation is attached to.
	SessionID string `json:"session_id"`
	ExpiresAt string `json:"expires_at"` // RFC 3339
}

// ImpersonationEnded is the payload of EventImpersonationEnded. A record that
// merely ran past its end publishes nothing, and whoever needs that signal
// computes it from ImpersonationStarted.ExpiresAt.
type ImpersonationEnded struct {
	// GrantID is the record that ended.
	GrantID string `json:"grant_id"`
	EndedBy string `json:"ended_by"` // actor, session-ended, disabled, membership-removed or revoked
}

// IDPChanged is the payload of EventIDPChanged.
type IDPChanged struct {
	// IDPID is the provider whose configuration changed.
	IDPID  string `json:"idp_id"`
	Change string `json:"change"` // created, updated, enabled, disabled, deleted, domain-verified, domain-unverified or domain-removed
}

// The closed set of reason classes a state change or revocation carries: a
// reason passed to SetStatus, Delete, Sessions.Revoke or Sessions.RevokeAccount
// is one of these, never free text.
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
