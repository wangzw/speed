package admin

import (
	"encoding/json"
	"time"
)

// The bodies this module's routes answer with. They are the fragment's
// components: the generated contract names each one after its Go type, so these
// names are part of what a client is built from. Each view is built field by
// field from the capability's value, so a field added to a stored row later does
// not reach a client by being there.

// AuthnAdminAccount is one account as the management routes show it.
type AuthnAdminAccount struct {
	_ struct{} `description:"One account as the management routes show it, built field by field so that a field added to the stored account does not reach a client by being there."`

	ID          string                 `json:"id" required:"true" description:"The account's opaque identifier: 1 to 64 bytes from [A-Za-z0-9._-], case-sensitive."`
	Kind        string                 `json:"kind" required:"true" enum:"user,service" description:"user for a human account, service for a non-interactive API client."`
	Status      string                 `json:"status" required:"true" enum:"disabled,active,deleted" description:"disabled, active or deleted; deleted is terminal and leaves a tombstone."`
	DisplayName string                 `json:"display_name" required:"true" description:"The account's display name, at most 100 characters and not used for any decision."`
	MFARequired bool                   `json:"mfa_required" required:"true" description:"True when the account must register a factor before it can sign in."`
	Protected   bool                   `json:"protected" required:"true" description:"True when the account may not be impersonated, independent of any Decider."`
	PasswordSet bool                   `json:"password_set" required:"true" description:"True when the account carries a password credential."`
	Attributes  map[string]string      `json:"attributes,omitempty" description:"Absent when the account carries no attribute set"`
	Identifiers []AuthnAdminIdentifier `json:"identifiers" required:"true" description:"The account's login identifiers, always an array and never null, empty when it has none. Sorted by kind in the fixed order username, email, phone, and within one kind by value ascending, so the first element is the one to name the account by."`
	CreatedAt   time.Time              `json:"created_at" required:"true" description:"When the account was created, as an RFC 3339 instant in UTC."`
}

// AuthnAdminIdentifier is one login identifier of an account as the management
// routes show it: the kind, the normalized value and whether its ownership was
// proved. It carries nothing else of the stored row — no identifier of its own,
// no verification instant and no named way of verification.
type AuthnAdminIdentifier struct {
	_ struct{} `description:"One login identifier bound to an account: its kind, its normalized value and whether its ownership was proved. It carries no identifier of its own, no verification instant and no named way of verification."`

	Kind           string `json:"kind" required:"true" enum:"username,email,phone" description:"The identifier kind: username, email or phone."`
	Value          string `json:"value" required:"true" description:"The identifier's value in the normalized form the account module stores: a username lower-cased, an email address trimmed and lower-cased, a phone number in E.164 with a leading plus sign and 7 to 15 digits."`
	Proven         bool   `json:"proven" required:"true" description:"True only when the identifier's ownership was proved: sms-code, email-code, invite or idp:<provider>. False for admin, import-unverified and self, which only record a fact an administrator, an import or the account's own holder asserted and are never grounds for automatic linking or an enterprise merge."`
	SignIn         bool   `json:"sign_in" required:"true" description:"True when the identifier is one the account signs in with. A username always is; an email or phone is only after it was proved and selected, and an identifier written under the old behaviour carries what that behaviour used."`
	Contact        bool   `json:"contact" required:"true" description:"True when the identifier is a contact of the account. A username never is; an email or phone is one the account selected, and an email written under the old behaviour is one."`
	DefaultContact bool   `json:"default_contact" required:"true" description:"True for the proved email address notifications to the account go to; at most one identifier of an account carries it."`
}

// AuthnAdminAccountEnvelope carries one account.
type AuthnAdminAccountEnvelope struct {
	_ struct{} `description:"A response carrying one account."`

	Account AuthnAdminAccount `json:"account" required:"true" description:"The account the response carries."`
}

// AuthnAdminAccountList is one page of accounts and the next cursor.
type AuthnAdminAccountList struct {
	_ struct{} `description:"One page of accounts and the cursor of the next page."`

	Accounts   []AuthnAdminAccount `json:"accounts" required:"true" description:"The accounts on this page, in ascending identifier order."`
	NextCursor string              `json:"next_cursor" required:"true" description:"The cursor of the next page, empty on the last page"`
}

// AuthnAdminFactor is one registered factor, without any credential.
type AuthnAdminFactor struct {
	_ struct{} `description:"One registered factor: its kind, label and status, and nothing of its credential."`

	Kind   string `json:"kind" required:"true" description:"The factor kind: totp, sms or webauthn."`
	Label  string `json:"label" required:"true" description:"The label the account gave the factor, for display only."`
	Status string `json:"status" required:"true" enum:"pending,active,disabled" description:"pending until the factor is confirmed, then active; disabled once it can no longer be used."`
}

// AuthnAdminFactorList is the factors registered on one account.
type AuthnAdminFactorList struct {
	_ struct{} `description:"The factors registered on one account."`

	Factors []AuthnAdminFactor `json:"factors" required:"true" description:"The account's registered factors; the empty array when it has none."`
}

// AuthnAdminRemoved is the number of factors a reset removed.
type AuthnAdminRemoved struct {
	_ struct{} `description:"The number of factors a multi-factor reset removed."`

	Removed int `json:"removed" required:"true" description:"The number of factors removed"`
}

// AuthnAdminSession is one session of an account, without any credential.
type AuthnAdminSession struct {
	_ struct{} `description:"One session of an account, without any credential a caller could use to take it over."`

	ID              string    `json:"id" required:"true" description:"The session's opaque identifier."`
	Kind            string    `json:"kind" required:"true" description:"The session kind: browser for a cookie session, token for an access and refresh token pair."`
	Assurance       uint8     `json:"assurance" required:"true" description:"0 none, 1 single factor, 2 multi-factor"`
	AuthTime        time.Time `json:"auth_time" required:"true" description:"When the session last authenticated, as an RFC 3339 instant in UTC."`
	ImpersonationID string    `json:"impersonation_id,omitempty" description:"Absent unless the session carries an impersonation"`
	CreatedAt       time.Time `json:"created_at" required:"true" description:"When the session was created, as an RFC 3339 instant in UTC."`
	LastSeenAt      time.Time `json:"last_seen_at" required:"true" description:"When a request last used the session, as an RFC 3339 instant in UTC."`
	ExpiresAt       time.Time `json:"expires_at" required:"true" description:"The session's absolute expiry, as an RFC 3339 instant in UTC."`
	Revoked         bool      `json:"revoked" required:"true" description:"True when the session has been revoked."`
	Current         bool      `json:"current" required:"true" description:"True when the row is the caller's own session"`
}

// AuthnAdminSessionList is the live sessions of one account.
type AuthnAdminSessionList struct {
	_ struct{} `description:"The live sessions of one account, most recently created first."`

	Sessions []AuthnAdminSession `json:"sessions" required:"true" description:"The account's live browser and token sessions, most recently created first; the empty array when it has none."`
}

// AuthnAdminRevoked is the number of sessions a bulk revocation ended.
type AuthnAdminRevoked struct {
	_ struct{} `description:"The number of sessions a bulk revocation ended."`

	Revoked int `json:"revoked" required:"true" description:"The number of sessions revoked"`
}

// AuthnAdminSecret is one service secret as the listing shows it.
type AuthnAdminSecret struct {
	_ struct{} `description:"One service secret as the listing shows it: identifier, label, expiry and revocation state, never the secret value."`

	ID        string    `json:"id" required:"true" description:"The secret's identifier, not the secret value."`
	Label     string    `json:"label" required:"true" description:"The label given to the secret when it was created."`
	ExpiresAt time.Time `json:"expires_at" required:"true" description:"When the secret expires, as an RFC 3339 instant in UTC; the zero instant 0001-01-01T00:00:00Z means it never expires."`
	Revoked   bool      `json:"revoked" required:"true" description:"True when the secret has been revoked."`
}

// AuthnAdminSecretList is the service secrets of one account.
type AuthnAdminSecretList struct {
	_ struct{} `description:"The service secrets of one account, oldest first."`

	Secrets []AuthnAdminSecret `json:"secrets" required:"true" description:"The account's service secrets, oldest first; the empty array when it has none."`
}

// AuthnAdminSecretCreated is a freshly minted service secret.
type AuthnAdminSecretCreated struct {
	_ struct{} `description:"A freshly minted service secret. The secret value appears in this response only."`

	ID        string    `json:"id" required:"true" description:"The new secret's identifier."`
	Label     string    `json:"label" required:"true" description:"The label given in the request."`
	Secret    string    `json:"secret" required:"true" description:"The secret itself, handed out once and never again"`
	ExpiresAt time.Time `json:"expires_at" required:"true" description:"The zero instant 0001-01-01T00:00:00Z when the request named no expiry"`
}

// AuthnAdminMember is one account's membership in one tenant.
type AuthnAdminMember struct {
	_ struct{} `description:"One account's membership in one tenant."`

	Account     string    `json:"account" required:"true" description:"The member account's identifier."`
	DisplayName string    `json:"display_name" required:"true" description:"The account's display name, at most 100 characters and not used for any decision; the empty string when the account has none."`
	Tenant      string    `json:"tenant" required:"true" description:"The tenant's identifier."`
	Active      bool      `json:"active" required:"true" description:"True when the membership is in force; false means suspended, which reads as not a member."`
	SSOExempt   bool      `json:"sso_exempt" required:"true" description:"True when the member is exempt from the tenant's enforced SSO requirement."`
	Source      string    `json:"source" required:"true" description:"How the membership came to exist: admin, initial, invite, import or jit:<provider identifier>."`
	Roles       []string  `json:"roles" required:"true" description:"The role names the account holds in the tenant, ascending and without duplicates; always an array and never null, empty when it holds none."`
	JoinedAt    time.Time `json:"joined_at" required:"true" description:"When the account joined the tenant, as an RFC 3339 instant in UTC."`
}

// AuthnAdminMemberList is one page of a tenant's memberships.
type AuthnAdminMemberList struct {
	_ struct{} `description:"One page of a tenant's memberships, in ascending account-identifier order, and the cursor of the next page."`

	Members    []AuthnAdminMember `json:"members" required:"true" description:"The memberships on this page; the empty array when the tenant has none."`
	NextCursor string             `json:"next_cursor" required:"true" description:"The cursor of the next page, empty on the last page"`
}

// AuthnAdminProviderDomain is one email domain a provider has claimed, with its
// verification state. A claim is written by the domain-claim route and becomes
// verified when the verification route finds its DNS TXT record.
type AuthnAdminProviderDomain struct {
	_ struct{} `description:"One email domain a provider has claimed, and whether its ownership was proved."`

	Domain   string `json:"domain" required:"true" description:"The domain in its normalized form: lower-case ASCII (punycode)."`
	Verified bool   `json:"verified" required:"true" description:"True when the domain's DNS TXT record proved ownership; only verified domains take part in sign-in discovery."`
}

// AuthnAdminProvider is one SSO provider. The encrypted client secret never
// appears.
type AuthnAdminProvider struct {
	_ struct{} `description:"One SSO provider. The encrypted client secret never appears, warnings names each setting that loosens a default, and domains lists every email domain the provider has claimed."`

	ID                string                     `json:"id" required:"true" description:"The provider's identifier."`
	Tenant            string                     `json:"tenant" required:"true" description:"The owning tenant's identifier; immutable after creation."`
	Protocol          string                     `json:"protocol" required:"true" description:"The protocol: oidc or saml."`
	Name              string                     `json:"name" required:"true" description:"The provider's display name; 1 to 64 characters."`
	Enabled           bool                       `json:"enabled" required:"true" description:"True when the provider may be used for sign-in."`
	Provisioning      string                     `json:"provisioning" required:"true" description:"jit creates an account on first sign-in; preprovisioned matches only an account an administrator has already created."`
	UsernameStrategy  string                     `json:"username_strategy" required:"true" description:"How a jit login determines the username it opens the account with: claim reads username_claim from the assertion, user-confirm asks the person before any account is written. Empty for a preprovisioned provider."`
	UsernameClaim     string                     `json:"username_claim" required:"true" description:"The claim a claim strategy reads: preferred_username or email (the address's local part). Empty for every other strategy."`
	TrustIDPMFA       bool                       `json:"trust_idp_mfa" required:"true" description:"True when the provider's assertion may count as a multi-factor proof, and only when its acr (or SAML AuthnContextClassRef) is in the configured list."`
	AllowIDPInitiated bool                       `json:"allow_idp_initiated" required:"true" description:"True when an unsolicited assertion from the provider is accepted; the route then warns that login CSRF cannot be defended against."`
	AllowSHA1         bool                       `json:"allow_sha1" required:"true" description:"True when SHA-1 signed assertions are accepted; the route then warns about the downgrade."`
	MergeExisting     bool                       `json:"merge_existing" required:"true" description:"The stored merge flag; it no longer opens merging, because a just-in-time login never merges into an existing account."`
	Config            json.RawMessage            `json:"config,omitempty" description:"The protocol's own configuration; absent when the provider carries none"`
	Version           int                        `json:"version" required:"true" description:"The provider's version, raised by every write."`
	Warnings          []string                   `json:"warnings,omitempty" description:"Absent when no setting loosens a default"`
	Domains           []AuthnAdminProviderDomain `json:"domains" required:"true" description:"Every email domain the provider has claimed, verified or not, in ascending domain order; always an array and never null, empty when it has none."`
}

// AuthnAdminProviderEnvelope carries one SSO provider.
type AuthnAdminProviderEnvelope struct {
	_ struct{} `description:"A response carrying one SSO provider."`

	Provider AuthnAdminProvider `json:"provider" required:"true" description:"The provider the response carries."`
}

// AuthnAdminProviderList is the SSO providers of one tenant.
type AuthnAdminProviderList struct {
	_ struct{} `description:"The SSO providers of one tenant."`

	Providers []AuthnAdminProvider `json:"providers" required:"true" description:"The tenant's providers; the empty array when it has none."`
}

// AuthnAdminVerifyToken is the token to publish in DNS.
type AuthnAdminVerifyToken struct {
	_ struct{} `description:"The token to publish in DNS to verify a claimed domain."`

	VerifyToken string `json:"verify_token" required:"true" description:"The token to publish in DNS"`
}

// AuthnAdminImpersonation is one impersonation record. The reason never appears
// here.
type AuthnAdminImpersonation struct {
	_ struct{} `description:"One impersonation record. The reason never appears here; it is in the record and the audit row alone."`

	ID        string    `json:"id" required:"true" description:"The record's identifier."`
	Actor     string    `json:"actor" required:"true" description:"The identifier of the account that started the impersonation."`
	Target    string    `json:"target" required:"true" description:"The identifier of the account being impersonated."`
	Tenant    string    `json:"tenant" required:"true" description:"The tenant the impersonation is confined to."`
	SessionID string    `json:"session_id" required:"true" description:"The identifier of the actor's session that carries the impersonation."`
	StartedAt time.Time `json:"started_at" required:"true" description:"When the impersonation started, as an RFC 3339 instant in UTC."`
	EndsAt    time.Time `json:"ends_at" required:"true" description:"When the impersonation lapses, as an RFC 3339 instant in UTC."`
	EndedAt   time.Time `json:"ended_at" required:"true" description:"The zero instant 0001-01-01T00:00:00Z while the record is still in force"`
	EndReason string    `json:"end_reason,omitempty" description:"Absent while the record is still in force"`
}

// AuthnAdminImpersonationList is one page of impersonation records.
type AuthnAdminImpersonationList struct {
	_ struct{} `description:"One page of impersonation records of one tenant, in ascending record-identifier order, and the cursor of the next page."`

	Impersonations []AuthnAdminImpersonation `json:"impersonations" required:"true" description:"The records on this page; the empty array when the tenant has none."`
	NextCursor     string                    `json:"next_cursor" required:"true" description:"The cursor of the next page, empty on the last page"`
}
