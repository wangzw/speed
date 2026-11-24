// Package tenant is the interface package of the tenancy capability: the tenant
// identifier, the roster's types and the model convention. It registers no module
// and imports nothing past the standard library.
package tenant

import (
	"database/sql/driver"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ID is a tenant's immutable identifier, the value a scoped table's tenant column
// holds. It is supplied by whoever creates the tenant, not generated here.
type ID string

// Value implements driver.Valuer and refuses to bind an identifier outside the
// domain, the empty one above all. It returns the string alongside the error: a
// caller that drops the error, as GORM does when keying rows, still gets the value.
func (id ID) Value() (driver.Value, error) {
	switch {
	case id == "":
		return string(id), fmt.Errorf("%w: an empty tenant identifier is being bound into a statement",
			ErrNoTenantInContext)
	case !IsIdentifier(string(id)):
		return string(id), fmt.Errorf("%w: a tenant identifier carrying a blank or a "+
			"control character is being bound into a statement", ErrInvalidTenant)
	}
	return string(id), nil
}

// IsIdentifier reports whether s is a value the identifier's domain admits:
// non-empty and free of blanks and control characters. Modules that layer a value
// by identifier restate this rule rather than import it.
func IsIdentifier(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

// nameShape matches the username form, because a personal tenant's name is its
// owner's username and the two share one namespace.
var nameShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}$`)

// IsName reports whether s is a value the name's domain admits: three to sixty-three
// lower-case letters, digits and dashes, beginning with a letter or a digit.
func IsName(s string) bool {
	return nameShape.MatchString(s)
}

// Status is a tenant's state. The set of values is closed.
type Status int

const (
	// StatusDisabled is the zero value: a status never written or not recognised by
	// this build reads as this, and the tenant is not served.
	StatusDisabled Status = iota
	// StatusActive is a tenant that is served.
	StatusActive
	// StatusDeleted is a tenant that has been deleted, a terminal state reached only
	// through Delete. The row stays as a tombstone.
	StatusDeleted
)

// String names the state for diagnostics and for the management surface's view.
func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusDeleted:
		return "deleted"
	default:
		return "disabled"
	}
}

// DisabledBy says who disabled a tenant. The set of values is closed.
type DisabledBy int

const (
	// DisabledByOperator is the zero value: an operator disabled the tenant, or the
	// source was never recorded or is not recognised, so that an unaccounted-for
	// disable is never revoked by an account's recovery.
	DisabledByOperator DisabledBy = iota
	// DisabledByAccount is a disable that account state caused; it is written only
	// through SetStatusByAccount.
	DisabledByAccount
)

// String names the source for diagnostics and for the management surface's view.
func (d DisabledBy) String() string {
	if d == DisabledByAccount {
		return "account"
	}
	return "operator"
}

// Profile is the organization profile a tenant carries. Its zero value is a tenant
// with no profile entered. The roster decides nothing from it.
type Profile struct {
	// DisplayName is the name the organization is shown by; it may be empty.
	DisplayName string
	// Description is a longer account of the organization.
	Description string
	// ContactEmail is the organization's contact address, not a login identity.
	ContactEmail string
	// Website is the organization's absolute http or https address.
	Website string
}

const (
	// DisplayNameMaxRunes bounds a display name.
	DisplayNameMaxRunes = 100
	// DescriptionMaxRunes bounds a description.
	DescriptionMaxRunes = 500
	// ContactEmailMaxBytes bounds a contact email.
	ContactEmailMaxBytes = 254
	// WebsiteMaxBytes bounds a website.
	WebsiteMaxBytes = 2048
)

// Normalize trims the surrounding whitespace from each field; whitespace inside a
// description is kept. Nothing else is rewritten.
func (p Profile) Normalize() Profile {
	return Profile{
		DisplayName:  strings.TrimSpace(p.DisplayName),
		Description:  strings.TrimSpace(p.Description),
		ContactEmail: strings.TrimSpace(p.ContactEmail),
		Website:      strings.TrimSpace(p.Website),
	}
}

// Validate judges a normalized profile against its bounds and shapes; the empty
// profile is valid. A failure carries ErrInvalidProfile and names the field, never
// the value. A contact email must be one bare address, and a website absolute,
// http or https, with a host and no userinfo.
func (p Profile) Validate() error {
	if n := utf8.RuneCountInString(p.DisplayName); n > DisplayNameMaxRunes {
		return fmt.Errorf("%w: the display name is %d runes, over its %d-rune bound",
			ErrInvalidProfile, n, DisplayNameMaxRunes)
	}
	if n := utf8.RuneCountInString(p.Description); n > DescriptionMaxRunes {
		return fmt.Errorf("%w: the description is %d runes, over its %d-rune bound",
			ErrInvalidProfile, n, DescriptionMaxRunes)
	}
	if len(p.ContactEmail) > ContactEmailMaxBytes {
		return fmt.Errorf("%w: the contact email is %d bytes, over its %d-byte bound",
			ErrInvalidProfile, len(p.ContactEmail), ContactEmailMaxBytes)
	}
	if p.ContactEmail != "" {
		address, err := mail.ParseAddress(p.ContactEmail)
		if err != nil || address.Address != p.ContactEmail {
			return fmt.Errorf("%w: the contact email is not one bare address", ErrInvalidProfile)
		}
	}
	if len(p.Website) > WebsiteMaxBytes {
		return fmt.Errorf("%w: the website is %d bytes, over its %d-byte bound",
			ErrInvalidProfile, len(p.Website), WebsiteMaxBytes)
	}
	if p.Website != "" {
		parsed, err := url.Parse(p.Website)
		if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
			(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
			return fmt.Errorf("%w: the website is not an absolute http or https address with a host and no userinfo",
				ErrInvalidProfile)
		}
	}
	return nil
}

// Tenant is one row of the roster.
type Tenant struct {
	// ID is the immutable identifier.
	ID ID
	// Name is what the request surface carries: a slug, unique across the roster,
	// chosen at creation and never changed.
	Name string
	// Status says whether this tenant is served, and whether it has been deleted.
	Status Status
	// DisabledBy says who disabled this tenant; it is read only while Status is
	// StatusDisabled.
	DisabledBy DisabledBy
	// OwnerAccount is the account that owns the organization; empty for the system
	// tenant.
	OwnerAccount string
	// Profile is the organization profile the tenant carries.
	Profile Profile
	// CreatedAt is when the tenant was registered, in UTC.
	CreatedAt time.Time
	// DeletedAt is when the tenant was written as deleted, or nil while the row is
	// not a tombstone.
	DeletedAt *time.Time
}
