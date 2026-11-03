package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
	"github.com/vislake/speed/pkg/tenant"
)

// ceilingLockKey is the transaction-scoped advisory lock the cross-channel
// ceiling is counted and charged under. The authentication capability takes the
// same key before it counts the ceiling and writes its ledger row, so the two
// sending channels serialise on one lock and not two. The value is written
// literally in both places; a test in this package pins it to the literal the
// authentication path uses.
const ceilingLockKey = "sms.budget:lock:ceiling"

// The windows a notification SMS attempt is budgeted over. Every window is a
// sliding one: the ledger answers a count of rows since an instant, so the count
// frees as the oldest row ages out. The hourly windows are one hour and the daily
// one a day.
const (
	hourlyWindow = time.Hour
	dailyWindow  = 24 * time.Hour
)

// The names the utilization metric carries, one per budget. They are the
// dimension attribute of the gauge, so an operator can tell a spent recipient
// budget from a spent tenant fan-out budget.
const (
	dimensionRecipientHourly      = "recipient-hourly"
	dimensionRecipientDaily       = "recipient-daily"
	dimensionTenantDistinctHourly = "tenant-distinct-hourly"
	dimensionAttemptHourly        = "attempt-hourly"
)

// smsAttempt reports whether one dispatch will attempt the sms channel: the
// recipient's effective channels include sms and this assembly delivered both
// sms seams. A dispatch that resolves to in_app alone, to email alone, or to an
// sms channel whose seam this assembly did not deliver makes no sms attempt and
// therefore spends none of the sms budgets.
//
// The preference matrix is read here, at submission time, which is the one read
// this module makes of it before the handler's own. A preference that changes
// between the two is not reconciled: the budget was charged for what the matrix
// said when the dispatch arrived.
func (p *pipeline) smsAttempt(ctx context.Context, d notification.Dispatch) (bool, error) {
	channels, err := p.prefs.Effective(ctx, d.Recipient.UserID, d.TypeKey)
	if err != nil {
		return false, fmt.Errorf("%s: resolving the effective channels of %q: %w", ModuleName, d.TypeKey, err)
	}
	if !p.seams.smsReady() {
		return false, nil
	}
	for _, ch := range channels {
		if ch == notification.ChannelSMS {
			return true, nil
		}
	}
	return false, nil
}

// budgetCheck is one window a dispatch is compared against.
type budgetCheck struct {
	query     budget.Query
	limit     int
	dimension string
	window    time.Duration
	// distinct counts different destinations rather than rows.
	distinct bool
}

// chargeSMSAttempt consults the shared cross-channel ceiling and this channel's
// own sms budgets in order and, when they all admit the attempt, writes one
// ledger row for it and answers its identifier. The row is the attempt: it is
// written before the carrier answers, so the consumer can mark it refunded when
// the message certainly did not leave the process.
//
// The cross-channel ceiling is consulted first. It is the one count both sending
// channels share, read from the ledger by CountCeiling, and the row written here
// spends it like an authentication attempt does. The count and the write run in
// one transaction under the same transaction-scoped advisory lock the
// authentication path takes (ceilingLockKey), so a notification attempt and an
// authentication attempt cannot both read the count below the ceiling and both
// commit a row. This module declares no ceiling of its own: the setting is the
// ledger module's sms.ceiling-hourly, and a second notification-only ceiling
// would only let the vendor bill two caps at once.
//
// The channel's own windows are then checked per recipient hourly, per recipient
// daily, per tenant distinct destination hourly and per tenant attempt hourly, in
// that order; the first one over stops the check and the later ones are not read.
// A refused dispatch writes no row, so it spends none of the budgets.
//
// A budget that cannot be read is an outage and not a pass: the error carries
// authn.ErrUnavailable, so the rejection direction is to refuse rather than to
// let an uncounted message through.
func (p *pipeline) chargeSMSAttempt(ctx context.Context, id tenant.ID, d notification.Dispatch) (budget.ID, error) {
	limits := p.settings.limits()
	now := time.Now().UTC()
	recipient := recipientHash(string(id), d.Recipient.UserID)

	hourly := budget.Query{
		Channel:   budget.ChannelNotification,
		Since:     now.Add(-hourlyWindow),
		Dimension: budget.DimensionDestination,
		Value:     recipient,
	}
	daily := hourly
	daily.Since = now.Add(-dailyWindow)

	tenantWindow := budget.Query{
		Channel:   budget.ChannelNotification,
		Since:     now.Add(-hourlyWindow),
		Dimension: budget.DimensionTenant,
		Value:     string(id),
	}
	attempts := tenantWindow
	attempts.IncludeRefunded = true

	checks := []budgetCheck{
		{query: hourly, limit: limits.perRecipientHourly, dimension: dimensionRecipientHourly, window: hourlyWindow},
		{query: daily, limit: limits.perRecipientDaily, dimension: dimensionRecipientDaily, window: dailyWindow},
		{query: tenantWindow, limit: limits.tenantDistinctHourly, dimension: dimensionTenantDistinctHourly, window: hourlyWindow, distinct: true},
		{query: attempts, limit: limits.attemptHourly, dimension: dimensionAttemptHourly, window: hourlyWindow},
	}

	var attempt budget.ID
	err := p.ledger.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := p.takeCeilingLock(tx); err != nil {
			return err
		}
		ledger := p.newLedger(tx)
		if limit := limits.ceilingHourly; limit > 0 {
			if err := p.throttleCeiling(ctx, ledger, limit, now.Add(-hourlyWindow), now); err != nil {
				return err
			}
		}
		for _, c := range checks {
			count, err := countBudget(ctx, ledger, c.query, c.distinct)
			if err != nil {
				return fmt.Errorf("%w: %s: counting a delivery budget: %w", authn.ErrUnavailable, ModuleName, err)
			}
			p.meters.observeBudget(ctx, d.TypeKey, c.dimension, count, int64(c.limit))
			if count < int64(c.limit) {
				continue
			}
			// The distinct budget bounds the different recipients a tenant reaches,
			// not the messages it sends: a destination already inside the window adds
			// no new one, so a repeat is admitted and the per-recipient budgets still
			// bound it.
			if c.distinct {
				present, err := ledger.Count(ctx, hourly)
				if err != nil {
					return fmt.Errorf("%w: %s: counting a delivery budget: %w", authn.ErrUnavailable, ModuleName, err)
				}
				if present > 0 {
					continue
				}
			}
			return &authn.ThrottledError{RetryAfter: retryAfter(ctx, ledger, c.query, c.window, now)}
		}

		userID := d.Recipient.UserID
		recorded, err := ledger.Record(ctx, budget.Attempt{
			Channel:        budget.ChannelNotification,
			Purpose:        d.TypeKey,
			DestinationMAC: recipient,
			TenantID:       string(id),
			AccountID:      &userID,
			AttemptedAt:    now,
			Outcome:        budget.OutcomePending,
		})
		if err != nil {
			return fmt.Errorf("%w: %s: recording a delivery attempt: %w", authn.ErrUnavailable, ModuleName, err)
		}
		attempt = recorded
		return nil
	})
	if err != nil {
		return 0, err
	}
	return attempt, nil
}

// takeCeilingLock takes the transaction-scoped advisory lock the cross-channel
// ceiling is charged under. The authentication path takes the same key inside its
// own send transaction, so a notification attempt waits for an authentication
// attempt's ceiling count and write to commit, and the other way round. SQLite
// serialises writers at the database itself, so there is no lock to take and the
// dialect check skips the statement.
func (p *pipeline) takeCeilingLock(tx *gorm.DB) error {
	if p.dialect != db.Postgres {
		return nil
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", ceilingLockKey).Error; err != nil {
		return fmt.Errorf("%w: %s: taking the cross-channel send lock: %w", authn.ErrUnavailable, ModuleName, err)
	}
	return nil
}

// throttleCeiling applies the cross-channel ceiling: the count of non-refunded,
// non-suppressed attempts both channels wrote in the window. A refunded attempt
// frees its share, which is why a definitive failure gives the ceiling back just
// as it gives the send budgets back. The oldest counted attempt is when the
// window frees a slot, and the refusal carries that moment as its retry. A ledger
// that cannot be read is an outage and not a pass.
func (p *pipeline) throttleCeiling(ctx context.Context, ledger budget.Store, limit int, since, now time.Time) error {
	n, err := ledger.CountCeiling(ctx, since)
	if err != nil {
		return fmt.Errorf("%w: %s: counting the cross-channel ceiling: %w", authn.ErrUnavailable, ModuleName, err)
	}
	p.meters.observeBudget(ctx, purposeShared, dimensionCeiling, n, int64(limit))
	if n < int64(limit) {
		return nil
	}
	retry := hourlyWindow
	oldest, ok, err := ledger.Oldest(ctx, budget.Query{Since: since})
	if err != nil {
		return fmt.Errorf("%w: %s: reading the oldest attempt for the cross-channel ceiling: %w", authn.ErrUnavailable, ModuleName, err)
	}
	if ok {
		if remaining := oldest.Add(hourlyWindow).Sub(now); remaining > 0 {
			retry = remaining
		} else {
			retry = time.Second
		}
	}
	return &authn.ThrottledError{RetryAfter: retry}
}

// countBudget reads one count: different destinations for a distinct budget,
// rows otherwise.
func countBudget(ctx context.Context, ledger budget.Store, q budget.Query, distinct bool) (int64, error) {
	if distinct {
		return ledger.CountDistinctDestinations(ctx, q)
	}
	return ledger.Count(ctx, q)
}

// retryAfter is how long the window a check fell into has left: when the oldest
// row the check counted ages out, the window has room again. A read that fails,
// and a window that holds no row, answers the whole window, which is the safe
// direction: a caller is told to wait rather than retrying at once. The delay is
// never below one second.
func retryAfter(ctx context.Context, ledger budget.Store, q budget.Query, window time.Duration, now time.Time) time.Duration {
	oldest, ok, err := ledger.Oldest(ctx, q)
	if err != nil || !ok {
		return window
	}
	wait := oldest.Add(window).Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}

// refundAttempt marks one charged attempt refunded: the message certainly did not
// reach the carrier, so it leaves the send budgets while it stays in the attempt
// budgets. A zero identifier names no row and is the no-sms-attempt case, and a
// refund failure is written to the module's log rather than failing the send: the
// row then keeps counting, which is the conservative direction, and retrying the
// job to refund it would send the message again.
func (p *pipeline) refundAttempt(ctx context.Context, id budget.ID) {
	if id <= 0 {
		return
	}
	if err := p.attempts.MarkRefunded(ctx, id, time.Now().UTC()); err != nil {
		p.logger.Error(ModuleName+": refunding a charged sms attempt failed",
			slog.Int64("attempt_id", int64(id)), slog.String("cause", err.Error()))
	}
}

// definitiveFailure reports whether a carrier's error is one that says the
// message certainly did not leave the process: a throttle, a rejection or a
// destination this deployment cannot route. An uncertain outcome is not
// definitive — the message may have gone out — so it keeps its send budget.
func definitiveFailure(err error) bool {
	switch {
	case errors.Is(err, mail.ErrThrottled), errors.Is(err, sms.ErrThrottled):
		return true
	case errors.Is(err, mail.ErrRejected), errors.Is(err, sms.ErrRejected):
		return true
	case errors.Is(err, sms.ErrUnroutable):
		return true
	default:
		return false
	}
}

// recipientHash is the digest one recipient's ledger rows are written under. It
// folds the tenant and the recipient identifier into a fixed-length digest: the
// identifier's own text can carry the separator a plain concatenation would use,
// and two tenants may use the same identifier for different accounts. The
// plaintext is never written to the ledger.
func recipientHash(tenantID, userID string) string {
	sum := sha256.Sum256([]byte(tenantID + "\x00" + userID))
	return hex.EncodeToString(sum[:])
}
