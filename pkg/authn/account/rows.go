package account

import (
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The tables this module owns. A migration file, a model below and a raw
// statement name each of them, and a change to one is a change to all three.
const (
	tableAccounts    = "authn_accounts"
	tableIdentifiers = "authn_identifiers"
	tablePending     = "authn_pending_contacts"
	tableExternals   = "authn_external_identities"
	tableMemberships = "authn_memberships"
	tableCredentials = "authn_password_credentials"
	tableServiceKeys = "authn_service_secrets"
	tableThrottles   = "authn_throttles"
)

// tables is every table this module owns, in the order Start looks for them.
var tables = []string{
	tableAccounts, tableIdentifiers, tablePending, tableExternals, tableMemberships,
	tableCredentials, tableServiceKeys, tableThrottles,
}

// The models below are the model path of the database module, which is what the
// statement-level audit capture sees. The two time columns the path would fill
// itself, from a clock of the machine's own zone, are switched off: every time
// is written explicitly, in UTC, through internal/clock.

// accountRow is one row of authn_accounts. A deleted account is a tombstone: the
// identifier, the kind, the status, the creation time and the deletion time hold
// something, and every other column that is not the validity instant or the time
// of the last write is empty.
type accountRow struct {
	ID               string     `gorm:"column:id;primaryKey"`
	Kind             string     `gorm:"column:kind"`
	Status           int16      `gorm:"column:status"`
	SignupState      int16      `gorm:"column:signup_state"`
	SignupVerifiedAt *time.Time `gorm:"column:signup_verified_at"`
	DisplayName      string     `gorm:"column:display_name"`
	Attributes       string     `gorm:"column:attributes"`
	MFARequired      bool       `gorm:"column:mfa_required"`
	Protected        bool       `gorm:"column:protected"`
	ValidAfter       time.Time  `gorm:"column:valid_after"`
	CreatedAt        time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
	LastLoginAt      *time.Time `gorm:"column:last_login_at"`
	DisabledAt       *time.Time `gorm:"column:disabled_at"`
	DeletedAt        *time.Time `gorm:"column:deleted_at"`
}

func (accountRow) TableName() string { return tableAccounts }

// pendingContactRow is one row of authn_pending_contacts: a contact an account
// reserved. verified_at is empty while the reservation is an unproved target a
// registration wrote, and set once the address is proved or an administrator
// writes it. A reservation is not an identifier and is read by no login path.
type pendingContactRow struct {
	ID         string     `gorm:"column:id;primaryKey"`
	AccountID  string     `gorm:"column:account_id"`
	Kind       string     `gorm:"column:kind"`
	Value      string     `gorm:"column:value"`
	VerifiedAt *time.Time `gorm:"column:verified_at"`
	CreatedAt  time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (pendingContactRow) TableName() string { return tablePending }

// identifierRow is one row of authn_identifiers.
type identifierRow struct {
	ID          string    `gorm:"column:id;primaryKey"`
	AccountID   string    `gorm:"column:account_id"`
	Kind        string    `gorm:"column:kind"`
	Value       string    `gorm:"column:value"`
	VerifiedAt  time.Time `gorm:"column:verified_at"`
	VerifiedVia string    `gorm:"column:verified_via"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (identifierRow) TableName() string { return tableIdentifiers }

// externalRow is one row of authn_external_identities.
type externalRow struct {
	ID            string     `gorm:"column:id;primaryKey"`
	AccountID     string     `gorm:"column:account_id"`
	Provider      string     `gorm:"column:provider"`
	Subject       string     `gorm:"column:subject"`
	Email         string     `gorm:"column:email"`
	EmailVerified bool       `gorm:"column:email_verified"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	LastLoginAt   *time.Time `gorm:"column:last_login_at"`
}

func (externalRow) TableName() string { return tableExternals }

// membershipRow is one row of authn_memberships. The primary key is the tenant
// and the account, and no statement on this table leaves the tenant out.
type membershipRow struct {
	TenantID  string    `gorm:"column:tenant_id;primaryKey"`
	AccountID string    `gorm:"column:account_id;primaryKey"`
	Status    int16     `gorm:"column:status"`
	Source    string    `gorm:"column:source"`
	SSOExempt bool      `gorm:"column:sso_exempt"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (membershipRow) TableName() string { return tableMemberships }

// secretRow is one row of authn_service_secrets. Only the SHA-256 of the secret
// is kept.
type secretRow struct {
	ID         string     `gorm:"column:id;primaryKey"`
	AccountID  string     `gorm:"column:account_id"`
	SecretHash string     `gorm:"column:secret_hash"`
	Label      string     `gorm:"column:label"`
	CreatedAt  time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	ExpiresAt  *time.Time `gorm:"column:expires_at"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
}

func (secretRow) TableName() string { return tableServiceKeys }

// The status column's numbers, which are authn.AccountStatus's.
const (
	statusDisabled int16 = 0
	statusActive   int16 = 1
	statusDeleted  int16 = 2
)

// The status column of a membership: the zero value is suspended.
const (
	membershipSuspended int16 = 0
	membershipActive    int16 = 1
)

// statusOf is the account status a row states, held to the three the column is
// allowed. A row the column's check admits reaches here and nothing else does;
// one written around the check reads as disabled, the zero value.
func statusOf(n int16) authn.AccountStatus {
	if n < statusDisabled || n > statusDeleted {
		return authn.StatusDisabled
	}
	return authn.AccountStatus(n)
}

// The signup_state column's numbers, which are authn.SignupState's.
const (
	signupNone     int16 = 0
	signupPending  int16 = 1
	signupVerified int16 = 2
)

// signupStateOf is the self-registration state a row states, held to the three
// the column is allowed. One written around the check reads as none, the zero
// value, so that a broken row is not taken for a registration that proved an
// address.
func signupStateOf(n int16) authn.SignupState {
	if n < signupNone || n > signupVerified {
		return authn.SignupNone
	}
	return authn.SignupState(n)
}
