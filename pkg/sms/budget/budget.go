// Package budget is the shared ledger of SMS attempts: one row for every
// message a sending channel spent budget on, and the counts both channels' budgets
// are read from.
package budget

import "time"

// Channel is the sending capability that made an attempt.
type Channel string

const (
	// ChannelAuthn is the authentication capability: an SMS verification code
	// for one of the login, mfa, enroll and reset purposes.
	ChannelAuthn Channel = "authn"
	// ChannelNotification is the notification capability: a notification
	// delivered over SMS.
	ChannelNotification Channel = "notification"
)

// Valid reports whether c is one of the two channels this ledger holds.
func (c Channel) Valid() bool {
	return c == ChannelAuthn || c == ChannelNotification
}

// Outcome is what was known about an attempt when it was written. The set is
// closed and no method rewrites it; only the refund time changes later.
type Outcome string

const (
	// OutcomePending is an attempt whose carrier answer is not known yet.
	OutcomePending Outcome = "pending"
	// OutcomeSent is an attempt the carrier accepted.
	OutcomeSent Outcome = "sent"
	// OutcomeUncertain is an attempt whose result cannot be told.
	OutcomeUncertain Outcome = "uncertain"
	// OutcomeThrottled is an attempt the carrier refused as throttled.
	OutcomeThrottled Outcome = "throttled"
	// OutcomeRejected is an attempt the carrier refused as defective.
	OutcomeRejected Outcome = "rejected"
	// OutcomeUnroutable is an attempt this deployment's carrier cannot carry at all.
	OutcomeUnroutable Outcome = "unroutable"
)

// Valid reports whether o is one of the outcomes this ledger records.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomePending, OutcomeSent, OutcomeUncertain,
		OutcomeThrottled, OutcomeRejected, OutcomeUnroutable:
		return true
	default:
		return false
	}
}

// Dimension names an indexed column a count can be scoped by; Channel is a
// Query field of its own, not a dimension.
type Dimension string

const (
	// DimensionSource is the hashed source identity, the per-source budgets column.
	DimensionSource Dimension = "source"
	// DimensionDestination is the hashed destination, the column a distinct count
	// reads.
	DimensionDestination Dimension = "destination"
	// DimensionTenant is the tenant the attempt belongs to.
	DimensionTenant Dimension = "tenant"
	// DimensionAccount is the hashed account the attempt is for; an attempt with no
	// account matches no account count.
	DimensionAccount Dimension = "account"
)

// Valid reports whether d is one of the indexed dimensions.
func (d Dimension) Valid() bool {
	return d == DimensionSource || d == DimensionDestination ||
		d == DimensionTenant || d == DimensionAccount
}

// ID identifies one ledger row. It is the value Record returns, and what
// MarkRefunded names.
type ID int64

// Attempt is one SMS attempt as it is written. SourceMAC and AccountID are nil
// when absent, DestinationMAC and AttemptedAt are required, and Suppressed marks a
// message the caller deliberately did not send, which must carry RefundedAt.
type Attempt struct {
	// Channel is the capability that made the attempt.
	Channel Channel
	// Purpose is the caller's purpose key.
	Purpose string
	// SourceMAC is the hashed source, nil when absent.
	SourceMAC *string
	// DestinationMAC is the hashed destination.
	DestinationMAC string
	// TenantID is the tenant, empty when not scoped.
	TenantID string
	// AccountID is the hashed account, nil when absent.
	AccountID *string
	// AttemptedAt is when budget was spent, in UTC.
	AttemptedAt time.Time
	// Outcome is what was known at the write.
	Outcome Outcome
	// RefundedAt is when the attempt was refunded, nil while charged.
	RefundedAt *time.Time
	// Suppressed marks a message the caller deliberately did not send.
	Suppressed bool
}

// Query describes the rows one count reads: Channel and Purpose filter, Dimension
// and Value scope by one column, and Since is inclusive. IncludeRefunded and
// OnlyRefunded are mutually exclusive.
type Query struct {
	// Channel filters by channel; empty means either.
	Channel Channel
	// Purpose filters by purpose; empty means every purpose.
	Purpose string
	// Dimension scopes the count by one indexed column.
	Dimension Dimension
	// Value is the dimension value to match.
	Value string
	// Since is the inclusive window start.
	Since time.Time
	// IncludeRefunded selects the attempt-budget shape.
	IncludeRefunded bool
	// OnlyRefunded selects the determinate-failure shape.
	OnlyRefunded bool
	// IncludeSuppressed admits suppressed rows.
	IncludeSuppressed bool
}
