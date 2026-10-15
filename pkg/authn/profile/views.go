package profile

import (
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
)

// The views the profile routes answer with. Each is built field by field from a
// root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// profileView is GET B/me's answer: the caller's own profile. It carries the
// account's self fields plus the read-only identifiers and external bindings,
// whose values are never written under this surface.
type profileView struct {
	AccountID   string            `json:"account_id"`
	Kind        string            `json:"kind"`
	DisplayName string            `json:"display_name"`
	MFARequired bool              `json:"mfa_required"`
	PasswordSet bool              `json:"password_set"`
	CreatedAt   string            `json:"created_at"`
	Attributes  map[string]string `json:"attributes"`
	Locale      string            `json:"locale"`
	TimeZone    string            `json:"time_zone"`
	Identifiers []identifierView  `json:"identifiers"`
	Links       []linkView        `json:"links"`
}

// identifierView is one identifier of the caller's account.
type identifierView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	VerifiedAt  string `json:"verified_at"`
	VerifiedVia string `json:"verified_via"`
}

// linkView is one external binding of the caller's account. It has no field for
// the subject, which is the provider's own name for the person and no part of
// what the account's holder is shown.
type linkView struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Email       string `json:"email,omitempty"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at,omitempty"`
}

// patchView is PATCH B/me's answer: the self-writable fields as they stand after
// the write — the display name, the whole attribute set, and the language and
// time zone read out of it.
type patchView struct {
	DisplayName string            `json:"display_name"`
	Attributes  map[string]string `json:"attributes"`
	Locale      string            `json:"locale"`
	TimeZone    string            `json:"time_zone"`
}

// profileViewOf builds the read view from the account module's read surface.
func profileViewOf(account authn.Account, identifiers []authn.Identifier, links []authn.ExternalBinding) profileView {
	attributes := copyAttributes(account.Attributes)
	view := profileView{
		AccountID:   string(account.ID),
		Kind:        string(account.Kind),
		DisplayName: account.DisplayName,
		MFARequired: account.MFARequired,
		PasswordSet: account.PasswordSet,
		CreatedAt:   instant(account.CreatedAt),
		Attributes:  attributes,
		Locale:      attributes[i18n.LocaleAttributeKey],
		TimeZone:    attributes[i18n.TimeZoneAttributeKey],
		Identifiers: make([]identifierView, 0, len(identifiers)),
		Links:       make([]linkView, 0, len(links)),
	}
	for _, i := range identifiers {
		view.Identifiers = append(view.Identifiers, identifierView{
			ID:          i.ID,
			Kind:        string(i.Kind),
			Value:       i.Value,
			VerifiedAt:  instant(i.VerifiedAt),
			VerifiedVia: i.VerifiedVia,
		})
	}
	for _, l := range links {
		view.Links = append(view.Links, linkView{
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
func patchViewOf(displayName string, attributes map[string]string) patchView {
	return patchView{
		DisplayName: displayName,
		Attributes:  copyAttributes(attributes),
		Locale:      attributes[i18n.LocaleAttributeKey],
		TimeZone:    attributes[i18n.TimeZoneAttributeKey],
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

// instant is t as RFC 3339 in UTC, and the empty string for the zero time.
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
