package delivery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// Dispatch validates, charges the delivery budgets and enqueues one dispatch and
// returns its job identifier; see notification.Dispatcher.
//
// The operations are fixed: shape, taxonomy, encode, the delivery budget, then
// enqueue. It renders no text and writes no ledger — the per-channel work
// happens when the job is consumed. The tenant comes from the context, and a
// context carrying none is refused with the tenant capability's own error
// before anything is written.
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
// The delivery budget is charged before the job is queued, so a producer that
// has begun to loop cannot fill the queue: once a window is spent, the dispatch
// is answered with a *authn.ThrottledError carrying Retry-After and nothing is
// enqueued. It is charged after the recipient's effective channels are resolved,
// because a message that goes nowhere outside the process — in_app alone, or an
// external channel this assembly cannot send on — must not spend a budget that
// exists to bound external sends. One delivery spends one unit per external
// channel it will attempt; the counters live in the key-value capability, one
// fixed window per bucket, and the limits are read from settings on every call.
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
	payload, err := json.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("%s: encoding the dispatch of %q: %w", ModuleName, d.TypeKey, err)
	}
	key, err := dispatchKey(id, d)
	if err != nil {
		return "", fmt.Errorf("%s: deriving the idempotency key of %q: %w", ModuleName, d.TypeKey, err)
	}
	deliveries, err := p.externalDeliveries(ctx, d)
	if err != nil {
		return "", err
	}
	if deliveries > 0 {
		if err := p.chargeBudgets(ctx, id, d, deliveries); err != nil {
			return "", err
		}
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
		return "", fmt.Errorf("%s: enqueuing the delivery of %q: %w", ModuleName, d.TypeKey, err)
	}
	return jobID, nil
}

// decodeDispatch reads a dispatch back out of a delivery job's payload. It is
// the inverse of the encoding Dispatch writes; a payload it cannot read is
// answered with notification.ErrInvalidRequest, which the handler treats as a
// validation failure.
func decodeDispatch(payload []byte) (notification.Dispatch, error) {
	var d notification.Dispatch
	if err := json.Unmarshal(payload, &d); err != nil {
		return notification.Dispatch{}, fmt.Errorf("%w: the delivery job's payload cannot be read: %w",
			notification.ErrInvalidRequest, err)
	}
	return d, nil
}
