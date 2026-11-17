package profile

import (
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
)

// The views the profile routes answer with. Each is built field by field from a
// root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there. The
// declaration names each one, so the fragment publishes the schema of exactly
// the shape the handler returns.

// AuthnProfileProfile is GET B/me's answer: the caller's own profile. It
// carries the account's self fields — the display name, the whole attribute set
// and the language, time zone and color mode read out of it — plus the read-only
// identifiers and external bindings, whose values are never written under this
// surface.
type AuthnProfileProfile struct {
	_ struct{} `description:"The caller's own profile: the account's self fields plus its identifiers and external bindings, which this surface only reads. It is built field by field from the account module's values, so a field added to the record there does not reach a client by being there."`

	AccountID    string                   `json:"account_id" required:"true" description:"The account's identifier, the one the request's identity names; never empty on a 200."`
	Kind         string                   `json:"kind" required:"true" description:"The account's kind: user or service, a service being a non-interactive API client."`
	DisplayName  string                   `json:"display_name" required:"true" description:"The account's display name, empty when it carries none"`
	MFARequired  bool                     `json:"mfa_required" required:"true" description:"Whether the account must register a factor"`
	PasswordSet  bool                     `json:"password_set" required:"true" description:"Whether the account has a password credential set"`
	CreatedAt    *time.Time               `json:"created_at" nullable:"true" required:"true" description:"When the account was created, as an RFC 3339 instant in UTC; null when the account carries no creation instant"`
	Attributes   map[string]string        `json:"attributes" required:"true" description:"Never null; the empty object when the account carries none"`
	Locale       string                   `json:"locale" required:"true" description:"Read out of the attribute set, empty when it carries none"`
	TimeZone     string                   `json:"time_zone" required:"true" description:"Read out of the attribute set, empty when it carries none"`
	ColorMode    string                   `json:"color_mode" required:"true" description:"Read out of the attribute set, empty when it carries none"`
	Identifiers  []AuthnProfileIdentifier `json:"identifiers" required:"true" description:"The account's identifiers; always an array, never null, and empty when the account has none. An email the account holds is among them even while it is unproved; a pending reservation is not an identifier and is not listed"`
	Verification AuthnProfileVerification `json:"verification" required:"true" description:"How far the account's email verification has got, read at request time and never stored in a session or an access token"`
	Links        []AuthnProfileLink       `json:"links" required:"true" description:"The account's external bindings; always an array, never null, and empty when there are none. The provider's own subject for the person is not included"`
}

// AuthnProfileVerification is the email-verification state as the profile shows
// it: the state, the address that state reports, and when the account first
// reached verified.
type AuthnProfileVerification struct {
	_ struct{} `description:"How far the account's email verification has got, read on this request: none when it has no email address to verify, pending when it holds an address that has not been proved, verified when it holds one whose ownership was proved."`

	State string     `json:"state" required:"true" description:"none, pending or verified. none means the account has no email address to verify; pending means it has an email address that has not been proved, whether the account itself, an administrator or an import wrote it; verified means an email address whose ownership was proved"`
	Email string     `json:"email" required:"true" description:"The address of the state: for verified the proved address verified earliest, ties broken by identifier; for pending the earliest unproved address or reservation awaiting verification, ties broken by identifier; empty for none"`
	Since *time.Time `json:"since" nullable:"true" required:"true" description:"When the account first reached verified, as an RFC 3339 instant in UTC; null while it never did, and for none"`
}

// AuthnProfileIdentifier is one identifier of the caller's account.
type AuthnProfileIdentifier struct {
	_ struct{} `description:"One identifier bound to the account, with the way its ownership was established."`

	ID             string    `json:"id" required:"true" description:"The identifier's own identifier"`
	Kind           string    `json:"kind" required:"true" description:"The identifier kind: username, email or phone"`
	Value          string    `json:"value" required:"true" description:"The identifier's value in the normalized form the account module stores: a username lower-cased, an email address trimmed and lower-cased, a phone number in E.164 with a leading plus sign and 7 to 15 digits"`
	VerifiedAt     time.Time `json:"verified_at" required:"true" description:"The instant the identifier's ownership was established, as an RFC 3339 instant in UTC; always present, as no identifier is stored awaiting verification"`
	VerifiedVia    string    `json:"verified_via" required:"true" description:"How the identifier's ownership was established: sms-code, email-code, invite, idp:<provider> (followed by the provider's identifier), admin, import-unverified or self. sms-code, email-code and idp:<provider> prove ownership; invite, admin, import-unverified and self only record a fact an invitation's link was delivered and presented, an administrator, an import or the account's own holder asserted"`
	Proven         bool      `json:"proven" required:"true" description:"Whether the way of verification proves ownership: true for sms-code, email-code and idp:<provider>, false for invite, admin, import-unverified and self"`
	SignIn         bool      `json:"sign_in" required:"true" description:"Whether the identifier is one the account signs in with. A username always is; an email or phone is only after it was proved and selected, and an identifier written under the old behaviour carries what that behaviour used"`
	Contact        bool      `json:"contact" required:"true" description:"Whether the identifier is a contact of the account. A username never is; an email or phone is one the account selected, and an email written under the old behaviour is one"`
	DefaultContact bool      `json:"default_contact" required:"true" description:"Whether this proved email address is the one notifications to the account go to; at most one identifier of an account carries it"`
}

// AuthnProfileLink is one external binding of the caller's account. It has no
// field for the subject, which is the provider's own name for the person and no
// part of what the account's holder is shown.
type AuthnProfileLink struct {
	_ struct{} `description:"One external identity bound to the account, as the account module keeps it; it carries no provider subject."`

	ID          string     `json:"id" required:"true" description:"The binding's own identifier"`
	Provider    string     `json:"provider" required:"true" description:"The provider the binding was made through, as the provider is registered"`
	Email       string     `json:"email,omitempty" description:"Absent when the provider gave no address"`
	CreatedAt   *time.Time `json:"created_at" nullable:"true" required:"true" description:"When the binding was created, as an RFC 3339 instant in UTC; null when the binding carries no creation instant"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty" description:"Absent when the binding has never been used to sign in"`
}

// AuthnProfilePatch is PATCH B/me's answer: the self-writable fields as they
// stand after the write — the display name, the whole attribute set, and the
// language, time zone and color mode read out of it.
type AuthnProfilePatch struct {
	_ struct{} `description:"The self-writable fields as they stand after a profile write, read back from what was stored."`

	DisplayName string            `json:"display_name" required:"true" description:"The display name as it stands after the write; the account's current one when the body carried none"`
	Attributes  map[string]string `json:"attributes" required:"true" description:"The whole attribute set as it stands, never null"`
	Locale      string            `json:"locale" required:"true" description:"Read out of the attribute set, empty when it carries none"`
	TimeZone    string            `json:"time_zone" required:"true" description:"Read out of the attribute set, empty when it carries none"`
	ColorMode   string            `json:"color_mode" required:"true" description:"Read out of the attribute set, empty when it carries none"`
}

// profileViewOf builds the read view from the account module's read surface.
func profileViewOf(account authn.Account, identifiers []authn.Identifier, links []authn.ExternalBinding,
	signup authn.SignupStatus,
) AuthnProfileProfile {
	attributes := copyAttributes(account.Attributes)
	view := AuthnProfileProfile{
		AccountID:    string(account.ID),
		Kind:         string(account.Kind),
		DisplayName:  account.DisplayName,
		MFARequired:  account.MFARequired,
		PasswordSet:  account.PasswordSet,
		CreatedAt:    instant(account.CreatedAt),
		Attributes:   attributes,
		Locale:       attributes[i18n.LocaleAttributeKey],
		TimeZone:     attributes[i18n.TimeZoneAttributeKey],
		ColorMode:    attributes[ColorModeAttributeKey],
		Identifiers:  make([]AuthnProfileIdentifier, 0, len(identifiers)),
		Verification: AuthnProfileVerification{State: signupStateName(signup.State), Email: signup.Email, Since: instant(signup.Since)},
		Links:        make([]AuthnProfileLink, 0, len(links)),
	}
	for _, i := range identifiers {
		view.Identifiers = append(view.Identifiers, AuthnProfileIdentifier{
			ID:             i.ID,
			Kind:           string(i.Kind),
			Value:          i.Value,
			VerifiedAt:     i.VerifiedAt.UTC(),
			VerifiedVia:    i.VerifiedVia,
			Proven:         authn.Verification{Via: i.VerifiedVia}.Proves(),
			SignIn:         i.SignIn,
			Contact:        i.Contact,
			DefaultContact: i.DefaultContact,
		})
	}
	for _, l := range links {
		view.Links = append(view.Links, AuthnProfileLink{
			ID:          l.ID,
			Provider:    l.Provider,
			Email:       l.Email,
			CreatedAt:   instant(l.CreatedAt),
			LastLoginAt: instant(l.LastLoginAt),
		})
	}
	return view
}

// patchViewOf builds the write view from the values now stored.
func patchViewOf(displayName string, attributes map[string]string) AuthnProfilePatch {
	return AuthnProfilePatch{
		DisplayName: displayName,
		Attributes:  copyAttributes(attributes),
		Locale:      attributes[i18n.LocaleAttributeKey],
		TimeZone:    attributes[i18n.TimeZoneAttributeKey],
		ColorMode:   attributes[ColorModeAttributeKey],
	}
}

// copyAttributes returns a copy of an attribute set, never the caller's map, and
// reads a set that is nil as the empty one so an answer is always an object.
func copyAttributes(attributes map[string]string) map[string]string {
	out := make(map[string]string, len(attributes))
	for k, v := range attributes {
		out[k] = v
	}
	return out
}

// signupStateName is the name the view writes for an account's email
// verification state: none, pending or verified. A value outside the declared
// set reads none, which is the zero state.
func signupStateName(s authn.SignupState) string {
	switch s {
	case authn.SignupPending:
		return "pending"
	case authn.SignupVerified:
		return "verified"
	default:
		return "none"
	}
}

// instant is t as an RFC 3339 instant in UTC, and nil for the zero time, which
// a view publishes as null.
func instant(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	at := t.UTC()
	return &at
}
