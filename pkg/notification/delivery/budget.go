package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// budgetKeyPrefix starts every budget counter's key. The key-value store has no
// namespace of its own, so each key carries the module name.
const budgetKeyPrefix = ModuleName + ":budget:"

// The bucket widths and key layouts of the budgets. A budget is a fixed window:
// one key per bucket, and a new bucket starts from zero. The layout names the
// bucket, so two dispatches in one window land on one key.
const (
	hourlyBucket = time.Hour
	dailyBucket  = 24 * time.Hour
	hourLayout   = "2006010215"
	dayLayout    = "20060102"
)

// externalDeliveries counts the external channels one dispatch will actually
// attempt: the recipient's effective channels, kept to those whose seams are
// present in this assembly. It is what decides whether a budget is charged at
// all. A dispatch that resolves to in_app alone, or to an external channel whose
// seam this assembly did not deliver, sends nothing outside the process and so
// spends none of the budgets; the same is true of a recipient who turned the
// external channels off.
//
// The preference matrix is read here, at submission time, which is the one read
// this module makes of it before the handler's own. A preference that changes
// between the two is not reconciled: the budget was charged for what the matrix
// said when the dispatch arrived.
func (p *pipeline) externalDeliveries(ctx context.Context, d notification.Dispatch) (int, error) {
	channels, err := p.prefs.Effective(ctx, d.Recipient.UserID, d.TypeKey)
	if err != nil {
		return 0, fmt.Errorf("%s: resolving the effective channels of %q: %w", ModuleName, d.TypeKey, err)
	}
	count := 0
	for _, ch := range channels {
		switch ch {
		case notification.ChannelEmail:
			if p.seams.emailReady() {
				count++
			}
		case notification.ChannelSMS:
			if p.seams.smsReady() {
				count++
			}
		}
	}
	return count, nil
}

// budgetCheck is one window a dispatch is charged against.
type budgetCheck struct {
	key    string
	limit  int
	bucket time.Duration
}

// chargeBudgets charges one dispatch against the per-recipient windows and then
// the deployment-wide one, in that order, and returns the first budget that the
// charge takes over as a throttled error. The increment happens before the
// comparison, so a refused dispatch still spends: counting only what was let
// through would let a caller already over its limit retry without paying.
//
// A budget that cannot be counted is an outage and not a pass: the error carries
// authn.ErrUnavailable, so the rejection direction is to refuse rather than to
// let an uncounted message through.
//
// One delivery is charged one unit per external channel it will attempt, so a
// dispatch that goes out under both email and sms spends two. The unit is the
// carrier call the budget exists to bound, not the dispatch.
func (p *pipeline) chargeBudgets(ctx context.Context, id tenant.ID, d notification.Dispatch, deliveries int) error {
	limits := p.settings.limits()
	now := time.Now().UTC()
	recipient := recipientBudgetSubject(id, d.Recipient.UserID)
	checks := []budgetCheck{
		{key: budgetKeyPrefix + "recipient-hourly:" + recipient + ":" + now.Format(hourLayout), limit: limits.perRecipientHourly, bucket: hourlyBucket},
		{key: budgetKeyPrefix + "recipient-daily:" + recipient + ":" + now.Format(dayLayout), limit: limits.perRecipientDaily, bucket: dailyBucket},
		{key: budgetKeyPrefix + "global-hourly:" + now.Format(hourLayout), limit: limits.globalHourly, bucket: hourlyBucket},
	}
	for _, c := range checks {
		count, err := p.counters.IncrByFloatWithTTL(ctx, c.key, float64(deliveries), c.bucket)
		if err != nil {
			return fmt.Errorf("%w: %s: counting a delivery budget: %w", authn.ErrUnavailable, ModuleName, err)
		}
		if count > float64(c.limit) {
			return &authn.ThrottledError{RetryAfter: untilNextBucket(now, c.bucket)}
		}
	}
	return nil
}

// recipientBudgetSubject is the key segment one recipient's counters live under.
// It folds the tenant and the recipient identifier into a fixed-length digest:
// the identifier's own text can carry the separator a plain concatenation would
// use, and two tenants may use the same identifier for different accounts.
func recipientBudgetSubject(id tenant.ID, userID string) string {
	sum := sha256.Sum256([]byte(string(id) + "\x00" + userID))
	return hex.EncodeToString(sum[:])
}

// untilNextBucket is how long the bucket a count fell into has left: the delay a
// throttled rejection states, never below one second so that a client does not
// retry in the same bucket.
func untilNextBucket(now time.Time, bucket time.Duration) time.Duration {
	next := now.Truncate(bucket).Add(bucket)
	wait := next.Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}
