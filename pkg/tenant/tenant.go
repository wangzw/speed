// Package tenant is the interface package of the tenant capability: the roster
// and resolution capabilities, the scoped-model convention and the tenant
// context accessors. It depends on the standard library only.
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

// ID is a tenant's immutable identifier. It is supplied by whoever creates the
// tenant.
type ID string

// Value implements driver.Valuer and refuses to bind an identifier outside the
// domain. The string is returned alongside the error because GORM drops the
// error when it keys rows by primary value.
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
// non-empty, and free of blanks and control characters.
func IsIdentifier(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

var nameShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}$`)

// IsName reports whether s is a value the name's domain admits: a slug of three
// to sixty-three lower-case letters, digits and dashes, beginning with a letter
// or a digit.
func IsName(s string) bool {
	return nameShape.MatchString(s)
}

// Status is a tenant's state. The set of values is closed.
type Status int

const (
	// StatusDisabled is the zero value; an unrecognised value reads as this.
	StatusDisabled Status = iota
	// StatusActive is a tenant that is served.
	StatusActive
	// StatusDeleted is a tenant that has been deleted, a tombstone. Only Delete
	// reaches this state.
	StatusDeleted
)

// String names the state for diagnostics and views.
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
	// DisabledByOperator is the zero value; an unrecognised value reads as this.
	DisabledByOperator DisabledBy = iota
	// DisabledByAccount is a disable that account state caused. It is written
	// only through SetStatusByAccount.
	DisabledByAccount
)

// String names the source for diagnostics and views.
func (d DisabledBy) String() string {
	if d == DisabledByAccount {
		return "account"
	}
	return "operator"
}

// Profile is the organization profile a tenant carries. Its zero value is a
// tenant with no profile entered.
type Profile struct {
	// DisplayName is the name the organization is shown by. It may be empty.
	DisplayName string
	// Description is a longer account of the organization.
	Description string
	// ContactEmail is the organization's contact address, one bare address.
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

// Normalize trims the surrounding whitespace from each field.
func (p Profile) Normalize() Profile {
	return Profile{
		DisplayName:  strings.TrimSpace(p.DisplayName),
		Description:  strings.TrimSpace(p.Description),
		ContactEmail: strings.TrimSpace(p.ContactEmail),
		Website:      strings.TrimSpace(p.Website),
	}
}

// Validate judges a normalized profile. The empty profile is valid. A failure
// wraps ErrInvalidProfile and names the field, never the value.
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
	// Name is the unique slug the request surface carries; it is never changed.
	Name string
	// Status says whether this tenant is served, and whether it has been deleted.
	Status Status
	// DisabledBy says who disabled this tenant. It is read only while Status is
	// StatusDisabled.
	DisabledBy DisabledBy
	// OwnerAccount is the account that owns the organization; empty when none.
	OwnerAccount string
	// Profile is the organization profile the tenant carries.
	Profile Profile
	// CreatedAt is when the tenant was registered, in UTC.
	CreatedAt time.Time
	// DeletedAt is when the tenant was written as deleted, or nil.
	DeletedAt *time.Time
}
