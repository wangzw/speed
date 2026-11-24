// Package tenant decides which tenant a request and the statements it issues
// belong to, keeps the roster of tenants that exist, and filters statements to
// the one they carry.
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

// ID is a tenant's immutable identifier. It is what a scoped table's tenant
// column holds and what logs, the startup account and error text name; the request
// surface carries a name the roster maps to an identifier of this kind.
type ID string

// Value implements driver.Valuer. It refuses to bind an empty identifier (ErrNoTenantInContext) or
// one carrying a blank or control character (ErrInvalidTenant). The string is returned beside the
// error because GORM panics on a nil primary value.
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
// non-empty, and free of blanks and control characters anywhere in it.
func IsIdentifier(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

var nameShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}$`)

// IsName reports whether s is a value the name's domain admits: the slug form a
// request surface carries, narrower than the identifier's.
func IsName(s string) bool {
	return nameShape.MatchString(s)
}

// Status is a tenant's state. The set of values is closed.
type Status int

const (
	// StatusDisabled is the zero value: a status column never written, or
	// holding a value this build does not recognise, reads as this, and the
	// tenant is not served.
	StatusDisabled Status = iota
	// StatusActive is a tenant that is served.
	StatusActive
	// StatusDeleted is a deleted tenant. The row, identifier and name stay, the
	// tenant is not served, and no write path changes its status again. Only
	// Delete reaches this state.
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
	// DisabledByOperator is the zero value: an operator disabled the tenant through SetStatus, or the
	// source was never recorded. An unrecognised source value reads as this too, so a disable nobody
	// can account for is never revoked by an account's recovery.
	DisabledByOperator DisabledBy = iota
	// DisabledByAccount is a disable that account state caused. It is written
	// only through SetStatusByAccount, and setting the account back to enabled
	// revokes it.
	DisabledByAccount
)

// String names the source for diagnostics and for the management surface's
// view. Only DisabledByAccount reads as the account's; every other value
// reads as the operator's.
func (d DisabledBy) String() string {
	if d == DisabledByAccount {
		return "account"
	}
	return "operator"
}

// Profile is the organization profile a tenant carries. Its zero value is a
// tenant with no profile entered. Its fields are descriptive and carry nothing
// the roster decides from.
type Profile struct {
	// DisplayName is the name the organization is shown by. It is free text and
	// may be empty, in which case a surface falls back to the name.
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

// Normalize trims surrounding whitespace from each field and keeps whitespace
// inside a description. Nothing else is rewritten.
func (p Profile) Normalize() Profile {
	return Profile{
		DisplayName:  strings.TrimSpace(p.DisplayName),
		Description:  strings.TrimSpace(p.Description),
		ContactEmail: strings.TrimSpace(p.ContactEmail),
		Website:      strings.TrimSpace(p.Website),
	}
}

// Validate judges a normalized profile against the domain this package states; the empty profile is
// valid. A failure carries ErrInvalidProfile and names the field, never the value. A contact email
// must be one bare address, and a website absolute http or https with a host.
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
	// Name is the slug the request surface carries, unique across the roster,
	// chosen when the tenant is created and never changed. Resolution replaces
	// it with the identifier before anything reads a tenant.
	Name string
	// Status says whether this tenant is served, and whether it has been
	// deleted.
	Status Status
	// DisabledBy says who disabled this tenant. It is read only while Status
	// is StatusDisabled, and enabling a tenant clears it.
	DisabledBy DisabledBy
	// OwnerAccount is the account that owns the organization. A personal
	// organization's owner is its account itself, and the system tenant carries
	// none.
	OwnerAccount string
	// Profile is the organization profile the tenant carries.
	Profile Profile
	// CreatedAt is when the tenant was registered, written by the roster in UTC.
	CreatedAt time.Time
	// DeletedAt is when the tenant was written as deleted, or nil while the row
	// is not a tombstone. Status stays the state a request is resolved by.
	DeletedAt *time.Time
}
