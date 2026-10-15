package delivery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// Dispatch validates and enqueues one dispatch and returns its job identifier;
// see notification.Dispatcher.
//
// The operations are fixed: shape, taxonomy, then enqueue. It resolves no
// effective channel, renders no text and writes no storage — the per-channel
// work happens when the job is consumed. The tenant comes from the context, and
// a context carrying none is refused with the tenant capability's own error
// before anything is written.
//
// The job's idempotency key is the per-channel formula with the channel
// dimension dropped, so two dispatches of one logical event — same tenant, type,
// recipient, language, occurrence and parameters — converge into one job and a
// repeated call returns the identifier of the job already there.
func (p *pipeline) Dispatch(ctx context.Context, d notification.Dispatch) (jobs.JobID, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("%s: %w", ModuleName, tenant.ErrNoTenantInContext)
	}
	if _, err := p.validate(d); err != nil {
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
