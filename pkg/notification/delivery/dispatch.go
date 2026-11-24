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

const deliveryTimeMarker = "$speed.time"

// deliveryPayload is the job payload: the dispatch and, when an sms
// attempt was charged, the ledger row that charge wrote.
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

func decodeDispatch(payload []byte) (notification.Dispatch, budget.ID, error) {
	var p deliveryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return notification.Dispatch{}, 0, fmt.Errorf("%w: the delivery job's payload cannot be read: %w",
			notification.ErrInvalidRequest, err)
	}
	return p.Dispatch, budget.ID(p.Attempt), nil
}
