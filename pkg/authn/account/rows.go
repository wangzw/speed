package account

import (
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// A migration file, the model's TableName and a raw statement all name each table;
// a change to one is a change to all three.
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

var tables = []string{
	tableAccounts, tableIdentifiers, tablePending, tableExternals, tableMemberships,
	tableCredentials, tableServiceKeys, tableThrottles,
}

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

type identifierRow struct {
	ID             string    `gorm:"column:id;primaryKey"`
	AccountID      string    `gorm:"column:account_id"`
	Kind           string    `gorm:"column:kind"`
	Value          string    `gorm:"column:value"`
	VerifiedAt     time.Time `gorm:"column:verified_at"`
	VerifiedVia    string    `gorm:"column:verified_via"`
	SignIn         bool      `gorm:"column:sign_in"`
	Contact        bool      `gorm:"column:contact"`
	DefaultContact bool      `gorm:"column:default_contact"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (identifierRow) TableName() string { return tableIdentifiers }

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

// membershipRow is one row of authn_memberships. No statement on this table leaves
// the tenant out.
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

const (
	statusDisabled int16 = 0
	statusActive   int16 = 1
	statusDeleted  int16 = 2
)

const (
	membershipSuspended int16 = 0
	membershipActive    int16 = 1
)

func statusOf(n int16) authn.AccountStatus {
	if n < statusDisabled || n > statusDeleted {
		return authn.StatusDisabled
	}
	return authn.AccountStatus(n)
}

const (
	signupNone     int16 = 0
	signupPending  int16 = 1
	signupVerified int16 = 2
)

func signupStateOf(n int16) authn.SignupState {
	if n < signupNone || n > signupVerified {
		return authn.SignupNone
	}
	return authn.SignupState(n)
}
