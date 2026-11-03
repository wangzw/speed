// Package budget is the shared ledger of SMS attempts: one row for every
// message a sending channel spent budget on, and the counts both channels' budgets
// are read from.
//
// The authentication capability (pkg/authn/sms) and the notification capability
// (pkg/notification/delivery) send over the same deployment's SMS capacity. A
// budget each keeps for itself lets the vendor bill two ceilings at once, and a
// source that changes the number it targets walks around a count kept per
// number. The ledger is the one place an attempt is written and the one place a
// count is read, so the cross-channel ceiling, a source's distinct destinations
// and a tenant's fan-out are measured over both channels together.
//
// A row is an attempt, not a delivery. The caller writes it when it spends its
// budget, before the carrier answers, and Outcome says what was known then. A
// certain failure — a rejection, an unroutable destination or a throttle —
// marks the row refunded with MarkRefunded: a send budget stops counting it,
// while an attempt budget keeps counting it. That distinction is the reason the
// counts are separate. Refunding a message the vendor refused must not hand an
// attacker a fresh budget, and charging a deployment for a message it never sent
// must not spend a real one.
//
// A second, independent kind of row is the suppressed attempt: a message the
// caller deliberately did not send, kept in the ledger so that a refusal cannot
// be told apart from an attempt that never left the process. A suppressed row is
// written with Suppressed set and refunded at write time, so it spends no send
// budget and no share of the cross-channel ceiling. It stays countable by a
// dimension count that names it, which is what keeps the point of refusal from
// revealing whether a destination was registered.
//
// The ledger holds no counter and no in-process state. Record is one INSERT and
// every count is a query over the table, so a lost update is not a shape this
// store can produce: concurrent records are concurrent inserts and every one of
// them is counted. Whether a budget is a hard cap is therefore the caller's
// transaction, not this store's. A caller that needs one runs its count and its
// Record in one transaction and serialises the dimension itself; the store locks
// nothing and the two statements are only ordered by the caller.
//
// The module declares one setting, the cross-channel hourly ceiling, because
// that count spans both sending channels and neither consumer owns it alone. It
// registers no other cap. It delivers Store and the table; the consumers own
// every other limit, the windows, and the response a throttled attempt receives.
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

// Outcome is what was known about an attempt when it was written.
//
// The set is closed, and an attempt carries the value known at the moment of
// the write. A caller that records the attempt before the carrier answers
// writes OutcomePending and, when the carrier answers with a certain failure,
// marks the row refunded; a caller that sends synchronously and knows the
// answer already writes the final value. No method rewrites Outcome: the refund
// is the state that changes later, and the outcome is the state the attempt had
// when it was charged.
type Outcome string

const (
	// OutcomePending is an attempt whose carrier answer is not known yet.
	OutcomePending Outcome = "pending"
	// OutcomeSent is an attempt the carrier accepted.
	OutcomeSent Outcome = "sent"
	// OutcomeUncertain is an attempt whose result cannot be told: the call may
	// have reached the carrier and may not.
	OutcomeUncertain Outcome = "uncertain"
	// OutcomeThrottled is an attempt the carrier refused as throttled.
	OutcomeThrottled Outcome = "throttled"
	// OutcomeRejected is an attempt the carrier refused as defective.
	OutcomeRejected Outcome = "rejected"
	// OutcomeUnroutable is an attempt this deployment's carrier cannot carry to
	// the destination at all.
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

// Dimension names an indexed column a count can be scoped by. Channel is not
// one: Query carries it as a field of its own, so a caller scopes by channel and
// by a dimension at once, which is the shape the four indexes are built for.
type Dimension string

const (
	// DimensionSource is the hashed source identity, the column the
	// per-source budgets count over.
	DimensionSource Dimension = "source"
	// DimensionDestination is the hashed destination, the column a distinct
	// count reads and the per-number budget scopes by.
	DimensionDestination Dimension = "destination"
	// DimensionTenant is the tenant the attempt belongs to, the column the
	// notification delivery's per-tenant fan-out counts over.
	DimensionTenant Dimension = "tenant"
	// DimensionAccount is the hashed account the attempt is for, the column
	// the per-account budget counts over. An attempt with no account — the
	// login and reset purposes, or a notification to a recipient the caller
	// did not name — carries no value here and matches no account count.
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

// Attempt is one SMS attempt as it is written.
//
// SourceMAC is the hashed source identity — the request's client address for
// the authentication channel, the producer for the notification channel — and
// is nil when the caller has no trustworthy source. A nil source is not an
// empty one: it takes no source budget and is counted by no source dimension.
// The plaintext the hash was computed over is never written here.
//
// DestinationMAC is the hashed destination and is required. TenantID is the
// tenant the attempt belongs to; an attempt that is not scoped to a tenant
// carries the empty string, and the tenant dimension matches no empty value.
// AccountID is nil when the attempt has no account, which is the case for the
// login and reset purposes and for a notification delivered to a recipient the
// caller did not name.
//
// AttemptedAt is the instant budget was spent, in UTC. Outcome is what was known
// then. RefundedAt is nil while the attempt is charged; a caller that knows at
// once that the message never reached the carrier may set it here instead of
// calling MarkRefunded afterwards, which is how a suppressed attempt — one that
// never leaves the process — is recorded as an attempt that spends no send
// budget.
//
// Suppressed marks a message the caller deliberately did not send. A suppressed
// attempt is refunded at write time: Record refuses one whose RefundedAt is nil,
// so the invariant is held by the store rather than left to every caller. The
// row spends no send budget and no share of the cross-channel ceiling, and it
// takes part in a count only when the query asks for it.
type Attempt struct {
	Channel        Channel
	Purpose        string
	SourceMAC      *string
	DestinationMAC string
	TenantID       string
	AccountID      *string
	AttemptedAt    time.Time
	Outcome        Outcome
	RefundedAt     *time.Time
	Suppressed     bool
}

// Query describes the rows one count reads. It is compiled into a WHERE clause
// by the SQL implementation and validated before it is used.
//
// Channel and Purpose are filters; each may be left empty, which means "either
// channel" and "every purpose". Dimension and Value scope the count by one
// indexed column; a Query with no dimension counts every row the filters and the
// window leave. Since is inclusive: a row written exactly at Since is inside the
// window. Every time is normalized to UTC before it reaches the database.
//
// Two switches then decide which rows the filters and the window leave count.
// IncludeSuppressed (false by default) decides whether a suppressed row takes
// part at all; a suppressed row is refunded at write time, so leaving it out is
// what keeps it from spending a send budget. The refund switch selects one of
// three shapes, stated here as predicates over a row:
//
//   - Send budget (neither IncludeRefunded nor OnlyRefunded): a charged row,
//     plus — when IncludeSuppressed is true — a suppressed row, which counts as
//     a charge in the dimension it was aimed at even though it was refunded.
//     The predicate is
//     "(refunded_at IS NULL OR suppressed)" when IncludeSuppressed is true, and
//     "refunded_at IS NULL AND NOT suppressed" when it is false.
//   - Attempt budget (IncludeRefunded): every row, refunded or not, with
//     "NOT suppressed" added when IncludeSuppressed is false. It has no refund
//     condition.
//   - Determinate failures (OnlyRefunded): rows refunded after the fact,
//     "refunded_at IS NOT NULL", with "NOT suppressed" added when
//     IncludeSuppressed is false. By default it therefore counts determinate
//     failures alone, and neither a successful send nor a suppressed row.
//
// OnlyRefunded and IncludeRefunded are mutually exclusive: one asks for the
// refunded subset and the other for every row, and a query naming both is
// refused rather than having one silently win. OnlyRefunded with
// IncludeSuppressed is legal and counts every refunded row, suppressed ones
// included; that is the one way to count a suppressed row as a refund.
type Query struct {
	Channel           Channel
	Purpose           string
	Dimension         Dimension
	Value             string
	Since             time.Time
	IncludeRefunded   bool
	OnlyRefunded      bool
	IncludeSuppressed bool
}
