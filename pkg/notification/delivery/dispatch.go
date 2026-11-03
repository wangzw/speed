package delivery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms/budget"
	"github.com/vislake/speed/pkg/tenant"
)

// deliveryPayload is the job payload: the dispatch and, when an sms attempt was
// charged, the ledger row that charge wrote. The consumer refunds the row when
// the message certainly did not leave the process. The field is omitted when the
// dispatch makes no sms attempt, so an in-app or email-only job carries no
// ledger identifier at all.
type deliveryPayload struct {
	Dispatch notification.Dispatch `json:"dispatch"`
	Attempt  int64                 `json:"attempt,omitempty"`
}

// Dispatch validates, charges the delivery budgets and enqueues one dispatch and
// returns its job identifier; see notification.Dispatcher.
//
// The operations are fixed: shape, taxonomy, encode, the delivery budgets, then
// enqueue. It renders no text and writes no per-channel ledger row — the
// per-channel work happens when the job is consumed. The tenant comes from the
// context, and a context carrying none is refused with the tenant capability's
// own error before anything is written.
//
// A dispatch that carries an external destination must carry the recipient's
// own: after the shape and taxonomy checks, and before a budget is charged, the
// destination is held against the identifiers the recipient's account holds
// through the account store, with the store's own normalization. A destination
// bound to another account, bound to no account, or named for a recipient that
// holds no address identifier is answered with notification.ErrInvalidRequest
// and nothing is enqueued. A dispatch that carries no destination is unaffected;
// a channel that needs one and finds none fails there, as before.
//
// Both external channels are budgeted before the job is queued, each on the
// store that fits it. An email delivery is charged on the key-value counters'
// fixed windows — per recipient hourly and daily, then deployment-wide hourly —
// because email has no ledger of its own. An sms attempt is counted over the
// shared SMS ledger, where the cross-channel ceiling and the channel's own
// per-recipient and per-tenant windows live, and one ledger row is written for
// it. A dispatch that resolves to in_app alone, or to an external channel whose
// seam this assembly did not deliver, spends nothing. A spent budget answers a
// *authn.ThrottledError carrying RetryAfter and nothing is enqueued. The job
// carries the sms row's identifier so the consumer can refund it on a certain
// failure.
//
// The job's idempotency key is the per-channel formula with the channel
// dimension dropped, so two dispatches of one logical event — same tenant, type,
// recipient, language, occurrence and parameters — converge into one job and a
// repeated call returns the identifier of the job already there. The budget is
// charged per dispatch and not per distinct event: the queue is what tells the
// two apart, and it says so only after the charge. A caller able to submit can
// vary the occurrence to create distinct jobs anyway, so the budget is the
// bound and the convergence is not.
func (p *pipeline) Dispatch(ctx context.Context, d notification.Dispatch) (jobs.JobID, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("%s: %w", ModuleName, tenant.ErrNoTenantInContext)
	}
	if _, err := p.validate(d); err != nil {
		return "", err
	}
	if err := p.checkDestination(ctx, d); err != nil {
		return "", err
	}
	key, err := dispatchKey(id, d)
	if err != nil {
		return "", fmt.Errorf("%s: deriving the idempotency key of %q: %w", ModuleName, d.TypeKey, err)
	}
	attempts, err := p.channelAttempts(ctx, d)
	if err != nil {
		return "", err
	}
	if attempts.email {
		if err = p.chargeEmailBudget(ctx, id, d); err != nil {
			return "", err
		}
	}
	var attempt budget.ID
	if attempts.sms {
		if attempt, err = p.chargeSMSAttempt(ctx, id, d); err != nil {
			return "", err
		}
	}
	payload, err := json.Marshal(deliveryPayload{Dispatch: d, Attempt: int64(attempt)})
	if err != nil {
		p.refundAttempt(ctx, attempt)
		return "", fmt.Errorf("%s: encoding the dispatch of %q: %w", ModuleName, d.TypeKey, err)
	}
	jobID, err := p.queue.Enqueue(ctx, jobs.Task{
		Type:           JobType,
		Payload:        payload,
		IdempotencyKey: key,
	},
		jobs.WithPriority(jobs.PriorityNormal),
		jobs.WithTimeout(jobTimeout),
		jobs.WithMaxRetries(jobMaxRetries),
	)
	if err != nil {
		p.refundAttempt(ctx, attempt)
		return "", fmt.Errorf("%s: enqueuing the delivery of %q: %w", ModuleName, d.TypeKey, err)
	}
	return jobID, nil
}

// decodeDispatch reads a dispatch and its charged attempt identifier back out of
// a delivery job's payload. It is the inverse of the encoding Dispatch writes; a
// payload it cannot read is answered with notification.ErrInvalidRequest, which
// the handler treats as a validation failure.
func decodeDispatch(payload []byte) (notification.Dispatch, budget.ID, error) {
	var p deliveryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return notification.Dispatch{}, 0, fmt.Errorf("%w: the delivery job's payload cannot be read: %w",
			notification.ErrInvalidRequest, err)
	}
	return p.Dispatch, budget.ID(p.Attempt), nil
}
