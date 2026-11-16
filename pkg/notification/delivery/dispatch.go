package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms/budget"
	"github.com/vislake/speed/pkg/tenant"
)

// deliveryTimeMarker is the reserved object key that carries a time parameter
// through the queue's JSON encoding. encoding/json writes a time.Time as a bare
// RFC 3339 string and decodes it back as a string, losing the type the rendering
// engine needs to expand a {{name, time, layout}} segment. The payload codec
// replaces each time with an object under this key and restores it when the job
// is read. A parameter map carrying this key is reserved to the codec.
const deliveryTimeMarker = "$speed.time"

// deliveryPayload is the job payload: the dispatch and, when an sms attempt was
// charged, the ledger row that charge wrote. The consumer refunds the row when
// the message certainly did not leave the process. The field is omitted when the
// dispatch makes no sms attempt, so an in-app or email-only job carries no
// ledger identifier at all.
//
// The type carries its own JSON codec so that a time parameter keeps its type
// across the queue (see deliveryTimeMarker); the fields are otherwise encoded as
// written.
type deliveryPayload struct {
	Dispatch notification.Dispatch `json:"dispatch"`
	Attempt  int64                 `json:"attempt,omitempty"`
}

// MarshalJSON encodes the payload with every time parameter replaced by a marked
// RFC 3339 string, so decoding can restore the type.
func (p deliveryPayload) MarshalJSON() ([]byte, error) {
	type wire struct {
		Dispatch notification.Dispatch `json:"dispatch"`
		Attempt  int64                 `json:"attempt,omitempty"`
	}
	d := p.Dispatch
	d.Params = markDeliveryTimes(d.Params)
	return json.Marshal(wire{Dispatch: d, Attempt: p.Attempt})
}

// UnmarshalJSON decodes the payload and restores every marked time parameter to
// a time.Time.
func (p *deliveryPayload) UnmarshalJSON(data []byte) error {
	var w struct {
		Dispatch notification.Dispatch `json:"dispatch"`
		Attempt  int64                 `json:"attempt,omitempty"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	p.Dispatch = w.Dispatch
	p.Dispatch.Params = unmarkDeliveryTimes(w.Dispatch.Params)
	p.Attempt = w.Attempt
	return nil
}

// markDeliveryTimes copies a parameter map with every time, including one nested
// in a map or a slice, replaced by a marked string.
func markDeliveryTimes(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	out := make(map[string]any, len(params))
	for name, value := range params {
		out[name] = markDeliveryTime(value)
	}
	return out
}

// markDeliveryTime marks one parameter value.
func markDeliveryTime(value any) any {
	switch v := value.(type) {
	case time.Time:
		return map[string]any{deliveryTimeMarker: v.Format(time.RFC3339Nano)}
	case map[string]any:
		out := make(map[string]any, len(v))
		for name, item := range v {
			out[name] = markDeliveryTime(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = markDeliveryTime(item)
		}
		return out
	default:
		return value
	}
}

// unmarkDeliveryTimes copies a decoded parameter map with every marked string
// restored to a time.Time.
func unmarkDeliveryTimes(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	out := make(map[string]any, len(params))
	for name, value := range params {
		out[name] = unmarkDeliveryTime(value)
	}
	return out
}

// unmarkDeliveryTime restores one parameter value. An object is a marked time
// only when it carries the marker key alone and the value parses as RFC 3339;
// anything else is walked as the object or slice it is.
func unmarkDeliveryTime(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 1 {
			if text, ok := v[deliveryTimeMarker].(string); ok {
				if instant, err := time.Parse(time.RFC3339Nano, text); err == nil {
					return instant
				}
			}
		}
		for name, item := range v {
			v[name] = unmarkDeliveryTime(item)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = unmarkDeliveryTime(item)
		}
		return v
	default:
		return value
	}
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
